// The tray panel. It talks to Go only through window.vitra.invoke; its
// grant lets it call glance.follow, coach.preset and panel.close, nothing
// else. It never sees the daemon's API token.
(function () {
  "use strict";

  var NAMES = { anthropic: "Claude", openai: "Codex", gemini: "Gemini", github: "Copilot", cursor: "Cursor",
    fireworks: "Fireworks", openrouter: "OpenRouter", deepseek: "DeepSeek", moonshot: "Moonshot", kimi: "Kimi",
    zai: "z.ai", minimax: "MiniMax" };
  // Two letters tell vendors apart where one would not (Claude, Codex,
  // Copilot, Cursor).
  var MARKS = { anthropic: "Cl", openai: "Cx", gemini: "Ge", github: "Co", cursor: "Cu" };
  var state = { view: null, selected: null };
  try { state.selected = localStorage.getItem("tokenops.tab"); } catch (e) { /* a private window */ }

  function invoke(command, args) { return window.vitra.invoke(command, args || {}, ""); }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = text;
    return e;
  }

  function name(p) { return NAMES[p] || (p ? p.charAt(0).toUpperCase() + p.slice(1) : "?"); }

  function level(pct) { return pct >= 80 ? "high" : pct >= 60 ? "medium" : "low"; }

  // Go durations ("136h35m0s") as "5d 16h".
  function seconds(s) {
    var m = /^(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?$/.exec(s || "");
    if (!m || !s) return 0;
    return (+(m[1] || 0)) * 3600 + (+(m[2] || 0)) * 60 + (+(m[3] || 0));
  }
  function human(sec) {
    var min = Math.round(sec / 60), d = Math.floor(min / 1440), h = Math.floor((min % 1440) / 60), mm = min % 60;
    if (d > 0) return d + "d " + h + "h";
    if (h > 0) return h + "h " + mm + "m";
    return mm + "m";
  }
  function money(n) { return "$" + (n >= 100 ? Math.round(n).toLocaleString() : n.toFixed(2)); }
  function tokens(n) {
    if (n >= 1e9) return (n / 1e9).toFixed(2) + "B";
    if (n >= 1e6) return Math.round(n / 1e6) + "M";
    if (n >= 1e3) return Math.round(n / 1e3) + "K";
    return String(n);
  }

  // Window names in words: "5h" is the session, "week (Fable)" Fable's week.
  function title(w) {
    var m = /^week \((.+)\)$/.exec(w.name);
    if (m) return m[1] + " · weekly";
    if (w.name === "week") return "Weekly";
    if (w.name === "5h") return "Session";
    if (w.name === "month") return "Monthly";
    return w.name.charAt(0).toUpperCase() + w.name.slice(1);
  }

  // Pace comes from the daemon (plans.WindowPace), the same answer the
  // CLI's cards give: behind lasts to the reset; ahead may run out first.
  function pace(w) {
    var p = w.pace;
    if (!p) return "";
    if (p.status === "on_pace") return "On pace";
    if (p.status === "behind") return "Pace: behind (" + Math.round(p.delta_pct) + "%) · lasts to reset";
    return "Pace: ahead (+" + Math.round(p.delta_pct) + "%) · " +
      (p.lasts_to_reset ? "lasts to reset" : "runs out in " + human((p.runs_out_in_ns || 0) / 1e9));
  }

  function busiest(r) {
    var m = r.spend_limit_usd > 0 ? r.spend_pct : 0;
    (r.windows || []).forEach(function (w) { m = Math.max(m, w.used_pct); });
    return m;
  }

  function meter(pct) {
    var track = el("div", "track");
    var fill = el("div", "fill " + level(pct));
    fill.style.width = Math.max(pct > 0 ? 2 : 0, Math.min(pct, 100)) + "%";
    track.appendChild(fill);
    return track;
  }

  function section(heading, pct, left, right, note) {
    var s = el("section", "metric");
    s.appendChild(el("h3", null, heading));
    s.appendChild(meter(pct));
    var line = el("div", "metric-line");
    line.appendChild(el("span", null, left));
    line.appendChild(el("span", "muted", right || ""));
    s.appendChild(line);
    if (note) s.appendChild(el("div", "note", note));
    return s;
  }

  function tabs(reports) {
    var nav = document.getElementById("tabs");
    nav.textContent = "";
    reports.forEach(function (r) {
      var b = el("button", "tab" + (r.provider === state.selected ? " active" : ""));
      b.type = "button";
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", r.provider === state.selected ? "true" : "false");
      var mark = el("span", "mark mark-" + r.provider, MARKS[r.provider] || name(r.provider).slice(0, 2));
      b.appendChild(mark);
      b.appendChild(el("span", "tab-name", name(r.provider)));
      var mini = el("span", "mini");
      var f = el("span", "mini-fill " + level(busiest(r)));
      f.style.width = Math.min(100, busiest(r)) + "%";
      mini.appendChild(f);
      b.appendChild(mini);
      b.addEventListener("click", function () {
        state.selected = r.provider;
        try { localStorage.setItem("tokenops.tab", r.provider); } catch (e) { /* ignore */ }
        render();
      });
      nav.appendChild(b);
    });
  }

  // The plan without its vendor's name: "Max 20x", "Pro Standard ($100)".
  function badge(r) {
    var d = r.display || r.plan_name || "";
    return d.replace(/^(Claude|ChatGPT|Gemini|GitHub Copilot|Cursor)\s+/, "");
  }

  function detail(r, v) {
    var main = document.getElementById("detail");
    main.textContent = "";
    var head = el("header", "head");
    var left = el("div");
    left.appendChild(el("h2", null, name(r.provider)));
    var age = v.updated ? Math.round((Date.now() - new Date(v.updated)) / 60000) : 0;
    left.appendChild(el("div", "muted", age < 1 ? "Updated just now" : "Updated " + age + "m ago"));
    head.appendChild(left);
    head.appendChild(el("span", "plan", badge(r)));
    main.appendChild(head);

    (r.windows || []).forEach(function (w) {
      main.appendChild(section(title(w), w.used_pct, Math.round(w.used_pct) + "% used",
        w.resets_in ? "Resets in " + human(seconds(w.resets_in)) : "", pace(w)));
    });
    if (r.spend_limit_usd > 0) {
      main.appendChild(section("Extra usage", r.spend_pct, "This month: " + money(r.spend_usd) + " / " + money(r.spend_limit_usd),
        Math.round(r.spend_pct) + "% used"));
    } else if (r.spend_usd > 0) {
      main.appendChild(el("p", "plain", "This month: " + money(r.spend_usd) + ", no limit known"));
    }
    if (r.balance_usd !== undefined && r.balance_usd !== null) {
      main.appendChild(el("p", "plain", money(r.balance_usd) + " credit left"));
    }
    var c = v.costs && v.costs[r.provider];
    if (c) {
      var cost = el("section", "metric cost");
      cost.appendChild(el("h3", null, "Cost"));
      var covered = c.last_30_days.cost_usd === 0 && c.last_30_days.api_equivalent_usd > 0;
      // Usage whose model has no list price yet is left out of the money:
      // mostly unpriced shows tokens only, partly unpriced says so.
      var share = function (s) { return s.requests ? s.unpriced_requests / s.requests : 0; };
      var line = function (label, s) {
        var amount = covered ? s.api_equivalent_usd : s.cost_usd;
        var t = tokens(s.tokens) + " tokens";
        if (share(s) > 0.5) return label + ": " + t;
        return label + ": " + money(amount) + (share(s) > 0.02 ? "+" : "") + " · " + t;
      };
      cost.appendChild(el("div", null, line("Today", c.today)));
      cost.appendChild(el("div", null, line("Last 30 days", c.last_30_days)));
      var s30 = c.last_30_days, notes = [];
      if (covered && share(s30) <= 0.5) notes.push("At API prices; your plan covers it.");
      if (share(s30) > 0.02) {
        notes.push(Math.round(share(s30) * 100) + "% of requests use models without a list price yet" +
          (share(s30) > 0.5 ? "." : ", left out of the $ figure."));
      }
      if (notes.length) cost.appendChild(el("div", "note", notes.join(" ")));
      main.appendChild(cost);
    }
  }

  // The coach's findings, ranked by the daemon: a mark by level, the
  // title, the figures behind it and what to do. They concern every plan,
  // so they follow whichever plan is shown.
  // withCode fills e with text, setting `quoted` parts as code.
  function withCode(e, text) {
    text.split("`").forEach(function (part, i) {
      if (!part) return;
      e.appendChild(i % 2 ? el("code", null, part) : document.createTextNode(part));
    });
    return e;
  }

  var MARKS_BY_LEVEL = { warn: "▲", notice: "●", info: "·" };
  function coachFindings(main, v) {
    var report = v.findings;
    if (!report || !report.findings) return;
    var box = el("section", "metric coach");
    var head = el("h3", null, "Coach");
    var count = report.findings.length;
    head.appendChild(el("span", "count", count ? String(count) : "nothing stands out"));
    box.appendChild(head);
    report.findings.forEach(function (f) {
      var item = el("div", "finding " + f.level);
      item.appendChild(el("span", "finding-mark", MARKS_BY_LEVEL[f.level] || "·"));
      var body = el("div", "finding-body");
      body.appendChild(el("div", "finding-title", f.title));
      if (f.evidence) body.appendChild(el("div", "note", f.evidence));
      if (f.action) body.appendChild(withCode(el("div", "finding-action"), f.action));
      item.appendChild(body);
      box.appendChild(item);
    });
    if (report.sessions_read_at) {
      var mins = Math.round((Date.now() - new Date(report.sessions_read_at)) / 60000);
      box.appendChild(el("div", "note", "Sessions read " + (mins < 60 ? mins + "m" : Math.round(mins / 60) + "h") + " ago."));
    }
    main.appendChild(box);
  }

  function render() {
    var v = state.view || {};
    var error = document.getElementById("error");
    var head = (v.glance && v.glance.plan_headroom) || {};
    var msg = v.error || (head.error ? (head.hint || head.error) : "");
    error.hidden = !msg;
    error.textContent = msg;
    var reports = (head.reports || []).slice().sort(function (a, b) { return busiest(b) - busiest(a); });
    if (!reports.some(function (r) { return r.provider === state.selected; }) && reports.length) {
      state.selected = reports[0].provider;
    }
    tabs(reports);
    var sel = reports.filter(function (r) { return r.provider === state.selected; })[0];
    if (sel) detail(sel, v); else document.getElementById("detail").textContent = "";
    coachFindings(document.getElementById("detail"), v);
    var select = document.getElementById("preset");
    if (v.coach) {
      var custom = document.getElementById("preset-custom");
      if (v.coach.preset) {
        if (custom) custom.remove();
        select.value = v.coach.preset;
      } else if (!custom) {
        custom = el("option", "", "Custom");
        custom.id = "preset-custom";
        custom.disabled = true;
        custom.selected = true;
        select.insertBefore(custom, select.firstChild);
      }
    }
  }

  function show(v) { state.view = v; render(); }

  window.vitra.on("glance.update", show);
  invoke("glance.follow").then(show, function (err) {
    show({ error: (err && err.message) || "the daemon could not be read" });
  });
  document.getElementById("preset").addEventListener("change", function (e) {
    invoke("coach.preset", { preset: e.target.value }).then(show, function (err) {
      state.view = state.view || {};
      state.view.error = "Coach not changed: " + ((err && err.message) || "refused");
      render();
    });
  });
  document.getElementById("close").addEventListener("click", function () { invoke("panel.close"); });
  document.addEventListener("keydown", function (e) { if (e.key === "Escape") invoke("panel.close"); });
})();
