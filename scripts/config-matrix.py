#!/usr/bin/env python3
"""Run the tokenops daemon under a matrix of configs, each isolated.

Every profile gets its own HOME, port, and store under a temporary
directory. Providers point at a local fake upstream that also acts as the
OTLP collector, so no request leaves the machine, except the public pricing
rate card in the one profile that leaves pricing refresh on. Four profiles
also drive `tokenops serve` over stdio and call every MCP tool once with
schema-minimal arguments.

Valid profiles must start, report healthy, proxy Anthropic and OpenAI
requests into the store, gate /api/* behind the token, and stop cleanly
with no ERROR, panic, or dropped rows in the log. Invalid profiles must be
refused at startup. Exits non-zero if any profile fails.

Usage: scripts/config-matrix.py [--bin bin/tokenops] [--only a,b] [--long] [--keep]

Optional real sources: TOKENOPS_MATRIX_OPENCODE_DB and
TOKENOPS_MATRIX_STATS_CACHE add the opencode and stats-cache readers to the
local-pollers profile. They are read only.
"""
import argparse, json, os, select, shutil, signal, socket, sqlite3, ssl, subprocess, sys, tempfile, threading, time
import urllib.request, urllib.error
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ARGS = None
RUNS = FIX = None
UP_PORT = None


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


CLAUDE_TURN = ('{"type":"assistant","timestamp":"%s","sessionId":"matrix-s1","message":{"id":"msg_matrix_%d",'
               '"model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":20,'
               '"cache_read_input_tokens":100,"cache_creation_input_tokens":5}}}')
CODEX_HEAD = ('{"timestamp":"%s","type":"session_meta","payload":{"id":"matrix-codex"}}\n'
              '{"timestamp":"%s","type":"turn_context","payload":{"model":"gpt-6-luna"}}\n')
CODEX_TURN = ('{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":'
              '{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,'
              '"total_tokens":%d},"model_context_window":258400}}}\n')


def write_fixtures(root):
    """Synthetic transcripts in the shapes the readers parse, dated now."""
    now = time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime())
    claude = os.path.join(root, "claude", "matrix-project")
    os.makedirs(claude)
    with open(os.path.join(claude, "matrix-s1.jsonl"), "w") as f:
        f.write("\n".join(CLAUDE_TURN % (now, i) for i in range(3)) + "\n")
    codex = os.path.join(root, "codex", *time.strftime("%Y/%m/%d", time.gmtime()).split("/"))
    os.makedirs(codex)
    with open(os.path.join(codex, "rollout-matrix.jsonl"), "w") as f:
        f.write(CODEX_HEAD % (now, now) + "".join(CODEX_TURN % (now, 100 + i, 110 + i) for i in range(3)))
OTLP_HITS = []


class Upstream(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        n = int(self.headers.get("content-length") or 0)
        raw = self.rfile.read(n)
        if self.path == "/v1/logs":
            OTLP_HITS.append(raw)
            return self._json(200, {})
        if self.path == "/v1/messages/count_tokens":
            return self._json(200, {"input_tokens": 7})
        if self.path == "/v1/messages":
            req = json.loads(raw or b"{}")
            return self._json(200, {
                "id": "msg_matrix_%d" % time.time_ns(), "type": "message", "role": "assistant",
                "model": req.get("model", "claude-sonnet-5"),
                "content": [{"type": "text", "text": "ok"}], "stop_reason": "end_turn",
                "usage": {"input_tokens": 11, "output_tokens": 3,
                          "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}})
        if self.path == "/v1/chat/completions":
            req = json.loads(raw or b"{}")
            return self._json(200, {
                "id": "chatcmpl-matrix", "object": "chat.completion", "model": req.get("model", "gpt-6-luna"),
                "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 9, "completion_tokens": 2, "total_tokens": 11}})
        return self._json(404, {"error": "unknown path " + self.path})


def yaml(obj, ind=0):
    pad = "  " * ind
    out = []
    for k, v in obj.items():
        if isinstance(v, dict):
            out.append(f"{pad}{k}:" + ("" if v else " {}"))
            if v:
                out.append(yaml(v, ind + 1))
        elif isinstance(v, list):
            out.append(f"{pad}{k}:" + ("" if v else " []"))
            for item in v:
                if isinstance(item, dict):
                    first = True
                    for ik, iv in item.items():
                        prefix = f"{pad}  - " if first else f"{pad}    "
                        out.append(f"{prefix}{ik}: {json.dumps(iv)}")
                        first = False
                else:
                    out.append(f"{pad}  - {json.dumps(item)}")
        else:
            out.append(f"{pad}{k}: {json.dumps(v)}")
    return "\n".join(out)


NO_POLLERS = {
    "claude_code": {"enabled": False}, "claude_code_jsonl": {"enabled": False},
    "codex_jsonl": {"enabled": False}, "opencode": {"enabled": False},
    "anthropic": {"enabled": False}, "github_copilot": {"enabled": False},
    "cursor": {"enabled": False}, "claude_usage_meter": {"enabled": False},
}
UP = None


def base(port, run):
    return {
        "mode": "passive", "listen": f"127.0.0.1:{port}",
        "log": {"level": "info", "format": "text"},
        "shutdown": {"timeout": "10s"},
        "providers": {"anthropic": UP, "openai": UP},
        "storage": {"enabled": True, "path": os.path.join(run, "events.db")},
        "rules": {"enabled": False},
        "pricing": {"refresh": {"disabled": True}},
        "vendor_usage": json.loads(json.dumps(NO_POLLERS)),
    }


def merge(a, b):
    for k, v in b.items():
        if isinstance(v, dict) and isinstance(a.get(k), dict):
            merge(a[k], v)
        else:
            a[k] = v
    return a


# name -> (overrides(run), expectations)
def profiles():
    P = {}
    P["minimal-passive"] = (lambda r: {}, {"mcp": True})
    P["active-budgets-watch"] = (lambda r: {
        "mode": "active",
        "plans": {"anthropic": "claude-max-20x", "openai": "gpt-plus"},
        "preferred_models": {"anthropic": "claude-opus-5"},
        "budgets": [{"name": "weekly-usd", "window": "weekly", "limit_usd": 50, "warn_at": 0.5, "crit_at": 0.9},
                    {"name": "daily-tokens", "window": "daily", "basis": "tokens", "limit_tokens": 1000000}],
        "watch": {"interval": "5s"},
    }, {"mcp": True})
    def pollers(r):
        vu = {
            "claude_code_jsonl": {"enabled": True, "root": os.path.join(FIX, "claude"), "interval": "5s"},
            "codex_jsonl": {"enabled": True, "root": os.path.join(FIX, "codex"), "interval": "5s"},
        }
        if os.environ.get("TOKENOPS_MATRIX_OPENCODE_DB"):
            vu["opencode"] = {"enabled": True, "root": os.environ["TOKENOPS_MATRIX_OPENCODE_DB"], "interval": "30s"}
        if os.environ.get("TOKENOPS_MATRIX_STATS_CACHE"):
            vu["claude_code"] = {"enabled": True, "path": os.environ["TOKENOPS_MATRIX_STATS_CACHE"], "interval": "30s"}
        return {"vendor_usage": vu}
    P["local-pollers"] = (pollers, {"sources": ["claude-code-jsonl", "codex-jsonl"]})
    P["tls"] = (lambda r: {"tls": {"enabled": True, "cert_dir": os.path.join(r, "certs"), "hostnames": ["localhost"]}},
                {"tls": True})
    P["otel-redact"] = (lambda r: {"otel": {"enabled": True, "endpoint": UP, "service_name": "matrix", "redact": True}},
                        {"otlp": True})
    P["otel-no-redact"] = (lambda r: {"otel": {"enabled": True, "endpoint": UP, "redact": False}}, {"otlp": True})
    P["resilience"] = (lambda r: {"resilience": {"enabled": True, "first_byte_timeout": "5s", "idle_timeout": "5s",
                                                 "total_timeout": "30s", "failure_threshold": 3}}, {})
    P["optimizer-automatic"] = (lambda r: {"optimizer": {
        "mode": "automatic", "routing_min_quality": 0.7,
        "routing_rules": [{"provider": "anthropic", "from_model": "claude-opus-5", "to_model": "claude-sonnet-5", "quality": 0.9}],
        "smart_routing": {"enabled": True, "intervention": "auto", "quality": 0.8, "window_pct_above": 50,
                          "models": {"anthropic": ["claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"]}},
        "command_fmt": {"default": "balanced", "emit_events": True}}}, {})
    P["optimizer-in-request"] = (lambda r: {"optimizer": {
        "mode": "in_request",
        "smart_routing": {"enabled": True, "intervention": "delegate", "auto_kinds": ["lookup", "research"],
                          "models": {"anthropic": ["claude-opus-5", "claude-sonnet-5"]}}}}, {})
    P["optimizer-off-advise"] = (lambda r: {"optimizer": {"mode": "off", "smart_routing": {
        "enabled": True, "intervention": "advise", "models": {"anthropic": ["claude-opus-5", "claude-sonnet-5"]}}}}, {})
    for d in ["observe", "advise", "intervene"]:
        P[f"coaching-{d}"] = (lambda r, d=d: {"coaching": {
            "delivery": d, "quiet": {"min_interval": "10m", "max_per_session": 3},
            "context_limits": [{"workflow_prefix": "claude-code:", "max_context_tokens": 200000,
                                "context_growth_limit_tokens": 50000, "max_consecutive_agent_loops": 20,
                                "system_redundancy_min": 3}]}}, {})
    if ARGS.long:
        P["retention"] = (lambda r: {
            "retention": {"interval": "6h", "keep": {"prompt": "1d", "workflow": "1d"},
                          "keep_by_source": {"codex-jsonl": "forever"}, "reclaim": True},
            "vendor_usage": {"claude_code_jsonl": {"enabled": True, "root": os.path.join(FIX, "claude")}},
        }, {"long": 330, "log_has": "retention prune"})
    P["pricing-refresh-on"] = (lambda r: {"pricing": {"refresh": {"disabled": False, "interval": "24h"}}}, {})
    P["storage-disabled"] = (lambda r: {"storage": {"enabled": False}, "rules": {"enabled": True, "root": REPO}},
                             {"no_store": True, "api_unauth_probe": "/api/rules/analyze", "mcp": True})
    P["rules-enabled"] = (lambda r: {"rules": {"enabled": True, "root": REPO, "repo_id": "matrix"}}, {"mcp": True})
    P["json-debug-admin-token"] = (lambda r: {"log": {"level": "debug", "format": "json"},
                                              "dashboard": {"admin_token": "matrix-token-123"}},
                                   {"token": "matrix-token-123"})
    P["coach-autonomous-quiet"] = (lambda r: {"coach": {"autonomy": "autonomous", "verbosity": "quiet"}}, {"mcp": True})
    P["coach-powers-verbose"] = (lambda r: {"coach": {"autonomy": "advise", "verbosity": "verbose",
                                                      "powers": {"waste": "autonomous", "models": "ask"}}}, {})
    P["coach-off"] = (lambda r: {"coach": {"autonomy": "off"}}, {})
    P["coach-context-autonomous"] = (lambda r: {"coach": {"autonomy": "advise", "powers": {"context": "autonomous"}},
                                                "coaching": {"context_limits": [{"workflow_prefix": "claude-code:",
                                                                                 "compact_at_tokens": 500000}]}}, {"mcp": True})
    P["plan-limits"] = (lambda r: {"plans": {"anthropic": "claude-max-20x"},
                                   "plan_limits": {"anthropic": {"spend_limit_usd": 200, "window": "monthly"}}}, {})
    # Invalid configs must be refused at startup with a clear message.
    bad = {
        "bad-mode": {"mode": "turbo"},
        "bad-log-level": {"log": {"level": "loud"}},
        "otel-no-endpoint": {"otel": {"enabled": True, "endpoint": ""}},
        "resilience-no-timeouts": {"resilience": {"enabled": True}},
        "unknown-provider": {"providers": {"nosuchai": UP}},
        "bad-plan": {"plans": {"anthropic": "claude-ultra-9000"}},
        "routing-quality-2": {"optimizer": {"routing_min_quality": 2}},
        "bad-delivery": {"coaching": {"delivery": "shout"}},
        "bad-optimizer-mode": {"optimizer": {"mode": "yolo"}},
        "negative-watch": {"watch": {"interval": "-5s"}},
        "bad-budget-window": {"budgets": [{"name": "b", "window": "hourly", "limit_usd": 5}]},
        "bad-smart-intervention": {"optimizer": {"smart_routing": {"enabled": True, "intervention": "yolo",
                                                                   "models": {"anthropic": ["claude-opus-5"]}}}},
        "empty-listen": {"listen": ""},
        "bad-coach-autonomy": {"coach": {"autonomy": "sometimes"}},
        "bad-coach-verbosity": {"coach": {"verbosity": "chatty"}},
        "bad-coach-power": {"coach": {"powers": {"billing": "advise"}}},
    }
    for k, v in bad.items():
        P["invalid-" + k] = (lambda r, v=v: v, {"invalid": True})
    return P


def req(url, method="GET", body=None, headers=None, ctx=None, timeout=5):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(r, timeout=timeout, context=ctx) as resp:
            return resp.status, resp.read().decode(errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")
    except Exception as e:  # noqa: BLE001
        return None, repr(e)


def minimal_args(schema):
    """Smallest arguments a tool's input schema accepts: required fields only."""
    if "enum" in schema:
        return schema["enum"][0]
    t = schema.get("type")
    if t == "string":
        return "matrix"
    if t in ("integer", "number"):
        lo = schema.get("minimum", schema.get("exclusiveMinimum", 0))
        return max(1, lo + (1 if "exclusiveMinimum" in schema else 0))
    if t == "boolean":
        return False
    if t == "array":
        return []
    if t == "object":
        req = schema.get("required", [])
        return {k: minimal_args(v) for k, v in schema.get("properties", {}).items() if k in req}
    return "matrix"


class MCPClient:
    """Minimal stdio JSON-RPC client for `tokenops serve`."""

    def __init__(self, run, cfgp, env):
        self.stderr = os.path.join(run, "serve.stderr")
        self.p = subprocess.Popen([ARGS.bin, "serve", "-c", cfgp], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=open(self.stderr, "a"), env=env, cwd=run)
        self.n = 0
        self.call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                 "clientInfo": {"name": "config-matrix", "version": "1"}})
        self._send({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def _send(self, msg):
        self.p.stdin.write((json.dumps(msg) + "\n").encode())
        self.p.stdin.flush()

    def call(self, method, params=None, timeout=60):
        self.n += 1
        msg = {"jsonrpc": "2.0", "id": self.n, "method": method}
        if params is not None:
            msg["params"] = params
        self._send(msg)
        end = time.time() + timeout
        while time.time() < end:
            ready, _, _ = select.select([self.p.stdout], [], [], end - time.time())
            if not ready:
                break
            line = self.p.stdout.readline()
            if not line:
                return {"_dead": True}
            try:
                m = json.loads(line)
            except ValueError:
                continue
            if m.get("id") == self.n:
                return m
        return {"_timeout": True}

    def close(self):
        self.p.stdin.close()
        try:
            self.p.wait(timeout=10)
            return True
        except subprocess.TimeoutExpired:
            self.p.kill()
            return False


def probe_mcp(run, cfgp, env, res):
    """Call every MCP tool once with schema-minimal arguments.

    A tool may refuse (a tool error carrying a message the agent can act
    on); it must not crash the server, hang, panic, or return a bare
    JSON-RPC error, which the agent receives as "internal error".
    """
    client = MCPClient(run, cfgp, env)
    tools = client.call("tools/list").get("result", {}).get("tools", [])
    if not tools:
        res["problems"].append("MCP tools/list returned no tools")
    refused = 0
    for tool in tools:
        name = tool["name"]
        started = time.time()
        r = client.call("tools/call", {"name": name, "arguments": minimal_args(tool.get("inputSchema", {"type": "object"}))})
        if r.get("_dead"):
            res["problems"].append(f"MCP {name}: server died")
            client = MCPClient(run, cfgp, env)
            continue
        if r.get("_timeout"):
            res["problems"].append(f"MCP {name}: no response in 60s")
            continue
        if "error" in r:
            code = r["error"].get("code")
            if code == -32602:
                refused += 1
            else:
                res["problems"].append(f"MCP {name}: JSON-RPC error {code} {r['error'].get('message', '')[:100]}")
            continue
        result = r.get("result", {})
        text = " ".join(c.get("text", "") for c in result.get("content", []) if isinstance(c, dict))
        if result.get("isError"):
            refused += 1
            if not text.strip():
                res["problems"].append(f"MCP {name}: tool error with no message")
        if "panic" in text.lower():
            res["problems"].append(f"MCP {name}: result mentions a panic")
        if time.time() - started > 20:
            res["problems"].append(f"MCP {name}: slow ({time.time() - started:.1f}s)")
    if not client.close():
        res["problems"].append("MCP serve did not exit when stdin closed")
    err = open(client.stderr).read()
    if "panic:" in err:
        res["problems"].append("MCP serve stderr has a panic")
    res["notes"].append(f"MCP {len(tools)} tools called, {refused} refused cleanly")


def run_profile(name, spec, port):
    over, exp = spec
    run = os.path.join(RUNS, name)
    shutil.rmtree(run, ignore_errors=True)
    os.makedirs(os.path.join(run, "home"))
    cfg = merge(base(port, run), over(run))
    cfgp = os.path.join(run, "config.yaml")
    with open(cfgp, "w") as f:
        f.write(yaml(cfg) + "\n")
    env = {k: v for k, v in os.environ.items() if not k.startswith("TOKENOPS_")}
    env["HOME"] = os.path.join(run, "home")
    # The XDG directories win over $HOME where set. Without these, serve's
    # config tools resolved the operator's real config.yaml through an
    # exported XDG_CONFIG_HOME: tokenops_vendor_usage_setup read its session
    # key, connected to claude.ai with it, and rewrote the file.
    for k in ("XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"):
        env[k] = env["HOME"]
    logf = open(os.path.join(run, "daemon.log"), "w")
    proc = subprocess.Popen([ARGS.bin, "start", "-c", cfgp], cwd=run, env=env, stdout=logf, stderr=subprocess.STDOUT)
    res = {"profile": name, "problems": [], "notes": []}
    scheme = "https" if exp.get("tls") else "http"
    ctx = None
    if exp.get("tls"):
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    url = f"{scheme}://127.0.0.1:{port}"

    if exp.get("invalid"):
        try:
            code = proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.send_signal(signal.SIGINT)
            proc.wait(timeout=15)
            res["problems"].append("ACCEPTED an invalid config (daemon started)")
            code = None
        logf.close()
        log = open(os.path.join(run, "daemon.log")).read()
        if code is not None:
            if code == 0:
                res["problems"].append("exited 0 on invalid config")
            last = [l for l in log.strip().splitlines() if l.strip()]
            res["notes"].append("refused: " + (last[-1][:150] if last else "(no message)"))
        return res

    up = False
    for _ in range(100):
        if proc.poll() is not None:
            break
        code, _ = req(url + "/healthz", ctx=ctx, timeout=1)
        if code == 200:
            up = True
            break
        time.sleep(0.2)
    if not up:
        proc.kill()
        logf.close()
        res["problems"].append("never became healthy; exit=%s; log tail: %s" % (
            proc.returncode, open(os.path.join(run, "daemon.log")).read()[-400:]))
        return res

    code, body = req(url + "/readyz", ctx=ctx)
    res["ready"] = f"{code} {body.strip()[:120]}"
    code, body = req(url + "/version", ctx=ctx)
    if code != 200:
        res["problems"].append(f"/version {code}")

    hdr = {"content-type": "application/json", "x-api-key": "matrix-dummy", "anthropic-version": "2023-06-01"}
    a_code, a_body = req(url + "/anthropic/v1/messages", "POST",
                         {"model": "claude-opus-5", "max_tokens": 8, "messages": [{"role": "user", "content": "hi"}]},
                         hdr, ctx)
    o_code, o_body = req(url + "/openai/v1/chat/completions", "POST",
                         {"model": "gpt-6-luna", "messages": [{"role": "user", "content": "hi"}]},
                         {"content-type": "application/json", "authorization": "Bearer matrix-dummy"}, ctx)
    c_code, _ = req(url + "/anthropic/v1/messages/count_tokens", "POST",
                    {"model": "claude-opus-5", "messages": [{"role": "user", "content": "hi"}]}, hdr, ctx)
    for label, code in (("anthropic", a_code), ("openai", o_code), ("count_tokens", c_code)):
        if code != 200:
            res["problems"].append(f"proxy {label} -> {code}")
    try:
        served = json.loads(a_body).get("model")
        if served != "claude-opus-5":
            res["notes"].append(f"anthropic request model rewritten to {served}")
    except Exception:  # noqa: BLE001
        pass

    tok = exp.get("token")
    if tok is None:
        p = os.path.join(run, "home", ".tokenops", "dashboard.token")
        tok = open(p).read().strip() if os.path.exists(p) else None
    if not exp.get("no_store"):
        code, _ = req(url + "/api/spend/summary", ctx=ctx)
        if code != 401:
            res["problems"].append(f"/api/spend/summary without token -> {code}, want 401")
        if tok:
            code, _ = req(url + "/api/spend/summary", headers={"authorization": "Bearer " + tok}, ctx=ctx)
            if code != 200:
                res["problems"].append(f"/api/spend/summary with token -> {code}, want 200")
    if exp.get("api_unauth_probe"):
        code, body = req(url + exp["api_unauth_probe"], ctx=ctx)
        res["notes"].append(f"{exp['api_unauth_probe']} without token -> {code}")
        if code == 200:
            res["problems"].append(f"{exp['api_unauth_probe']} served WITHOUT auth when storage is disabled")

    if exp.get("mcp"):
        probe_mcp(run, cfgp, env, res)
    time.sleep(exp.get("long", 8))
    proc.send_signal(signal.SIGINT)
    try:
        rc = proc.wait(timeout=25)
    except subprocess.TimeoutExpired:
        proc.kill()
        rc = "killed"
        res["problems"].append("did not stop within 25s of SIGINT")
    logf.close()
    log = open(os.path.join(run, "daemon.log")).read()
    if rc not in (0,):
        res["problems"].append(f"exit code {rc}")
    for bad in ("panic:", "level=ERROR", '"level":"ERROR"', "gave up", "DATA RACE"):
        n = log.count(bad)
        if n:
            lines = [l for l in log.splitlines() if bad in l][:2]
            res["problems"].append(f"log has {n}x {bad!r}: " + " | ".join(l[:180] for l in lines))
    warns = [l for l in log.splitlines() if "level=WARN" in l or '"level":"WARN"' in l]
    if warns:
        res["notes"].append(f"{len(warns)} WARN, first: {warns[0][:160]}")
    if exp.get("log_has") and exp["log_has"] not in log:
        res["problems"].append(f"log missing {exp['log_has']!r}")

    dbp = os.path.join(run, "events.db")
    if exp.get("no_store"):
        if os.path.exists(dbp):
            res["problems"].append("store file created with storage disabled")
    elif os.path.exists(dbp):
        db = sqlite3.connect(dbp)
        by = dict(db.execute("select source, count(*) from events group by source").fetchall())
        db.close()
        res["events"] = by
        if not any(k for k in by if "proxy" in k or k in ("anthropic", "openai")) and sum(by.values()) < 2:
            res["problems"].append(f"proxied requests not stored: {by}")
        for s in exp.get("sources", []):
            if not by.get(s):
                res["problems"].append(f"no events from {s}")
        mode = oct(os.stat(dbp).st_mode & 0o777)
        if mode != "0o600":
            res["problems"].append(f"store mode {mode}")
    else:
        res["problems"].append("store file missing")
    if exp.get("otlp"):
        if not OTLP_HITS:
            res["problems"].append("OTLP collector received nothing")
        else:
            blob = b"".join(OTLP_HITS)
            res["notes"].append(f"OTLP posts={len(OTLP_HITS)}")
            if b"matrix-dummy" in blob:
                res["problems"].append("credential leaked into OTLP export")
        OTLP_HITS.clear()
    return res


def main():
    global ARGS, RUNS, FIX, UP_PORT, UP
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--bin", default=os.path.join(REPO, "bin", "tokenops"))
    ap.add_argument("--only", default="", help="comma-separated profile names")
    ap.add_argument("--long", action="store_true", help="include the 5.5-minute retention profile")
    ap.add_argument("--keep", action="store_true", help="keep run directories for inspection")
    ARGS = ap.parse_args()
    ARGS.bin = os.path.abspath(ARGS.bin)
    if not os.access(ARGS.bin, os.X_OK):
        sys.exit(f"not an executable: {ARGS.bin} (run make build)")
    only = {n for n in ARGS.only.split(",") if n}
    work = tempfile.mkdtemp(prefix="tokenops-matrix-")
    RUNS, FIX = os.path.join(work, "runs"), os.path.join(work, "fixtures")
    os.makedirs(RUNS)
    write_fixtures(FIX)
    UP_PORT = free_port()
    UP = f"http://127.0.0.1:{UP_PORT}"
    srv = ThreadingHTTPServer(("127.0.0.1", UP_PORT), Upstream)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    P = profiles()
    unknown = only - set(P)
    if unknown:
        sys.exit(f"unknown profiles: {sorted(unknown)}")
    failed = []
    for n in [n for n in P if not only or n in only]:
        r = run_profile(n, P[n], free_port())
        status = "FAIL" if r["problems"] else "ok"
        if r["problems"]:
            failed.append(n)
        print(f"[{status:4}] {n}", flush=True)
        for p in r["problems"]:
            print(f"        x {p}", flush=True)
        for note in r["notes"]:
            print(f"        . {note}", flush=True)
        if r.get("events"):
            print(f"        . events {r['events']}", flush=True)
    srv.shutdown()
    if ARGS.keep:
        print(f"runs kept in {RUNS}")
    else:
        shutil.rmtree(work, ignore_errors=True)
    print(f"{len(failed)} failed" + (": " + ", ".join(failed) if failed else ""))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
