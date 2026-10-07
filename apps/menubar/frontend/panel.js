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
  // CLI's cards give. delta_pct is the share used minus the share of the
  // window elapsed: behind is reserve, ahead is over pace.
  function paceWord(p) {
    if (!p) return "";
    if (p.status === "on_pace") return "On pace";
    if (p.status === "used_up") return "Used up";
    var d = Math.abs(Math.round(p.delta_pct));
    return p.status === "behind" ? d + "% in reserve" : d + "% over pace";
  }
  function paceOutlook(p, used) {
    if (used >= 100) return "Used up until reset";
    if (!p) return "";
    if (p.lasts_to_reset || p.status === "behind") return "Lasts until reset";
    return p.runs_out_in_ns ? "Runs out in " + human(p.runs_out_in_ns / 1e9) : "";
  }

  function busiest(r) {
    var m = r.spend_limit_usd > 0 ? r.spend_pct : 0;
    (r.windows || []).forEach(function (w) { m = Math.max(m, w.used_pct); });
    return m;
  }

  function leftOf(used) { return Math.max(0, Math.min(100, 100 - used)); }

  // The bar fills to the share left, coloured by how much is used. The
  // tick is where an even spread would leave it: fill short of the tick is
  // over pace, past it is reserve.
  function meter(used, p) {
    var track = el("div", "track");
    var fill = el("div", "fill " + level(used));
    fill.style.width = leftOf(used) + "%";
    track.appendChild(fill);
    if (p) {
      var tick = el("div", "tick");
      tick.style.left = leftOf(used - p.delta_pct) + "%";
      track.appendChild(tick);
    }
    return track;
  }

  function pair(cls, a, b) {
    var line = el("div", cls);
    line.appendChild(el("span", null, a));
    line.appendChild(el("span", "right", b || ""));
    return line;
  }

  // stale is a window whose source has stopped (ADR 0011): its share is
  // the last one read, so its age stands where its pace would.
  function windowSection(heading, used, resets, p, stale) {
    var s = el("section", "metric" + (stale ? " stale" : ""));
    s.appendChild(el("h3", null, heading));
    s.appendChild(meter(used, stale ? null : p));
    s.appendChild(pair("metric-line strong", Math.round(leftOf(used)) + "% left", resets));
    if (stale) {
      s.appendChild(pair("metric-line warn", "Reading from " + human(stale) + " ago", ""));
      return s;
    }
    var word = paceWord(p), outlook = paceOutlook(p, used);
    if (word || outlook) s.appendChild(pair("metric-line", word, outlook));
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
      b.appendChild(el("span", "mark mark-" + r.provider, MARKS[r.provider] || name(r.provider).slice(0, 2)));
      b.appendChild(el("span", "tab-name", name(r.provider)));
      var mini = el("span", "mini");
      var f = el("span", "mini-fill " + level(busiest(r)));
      f.style.width = leftOf(busiest(r)) + "%";
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

  function stat(label, value) {
    var s = el("div", "stat");
    s.appendChild(el("div", "stat-label", label));
    s.appendChild(el("div", "stat-value", value));
    return s;
  }

  // The last 30 days, a bar per day, days without usage left empty.
  function chart(daily) {
    var byDate = {};
    (daily || []).forEach(function (d) { byDate[d.date] = d.tokens; });
    var days = [], max = 0;
    for (var i = 29; i >= 0; i--) {
      var t = new Date(Date.now() - i * 86400000);
      var key = t.getFullYear() + "-" + String(t.getMonth() + 1).padStart(2, "0") + "-" + String(t.getDate()).padStart(2, "0");
      var n = byDate[key] || 0;
      days.push({ key: key, n: n });
      max = Math.max(max, n);
    }
    var box = el("div", "chart");
    box.setAttribute("role", "img");
    box.setAttribute("aria-label", "Tokens per day over the last 30 days");
    days.forEach(function (d) {
      var bar = el("div", "bar");
      bar.style.height = (max ? Math.max(d.n ? 3 : 0, d.n / max * 100) : 0) + "%";
      bar.title = d.key + ": " + tokens(d.n) + " tokens";
      box.appendChild(bar);
    });
    return box;
  }

  // Usage whose model has no list price yet is left out of the money:
  // mostly unpriced shows no money, partly unpriced marks it with "+".
  function share(s) { return s.requests ? s.unpriced_requests / s.requests : 0; }
  function amount(s, covered) {
    if (share(s) > 0.5) return "—";
    return money(covered ? s.api_equivalent_usd : s.cost_usd) + (share(s) > 0.02 ? "+" : "");
  }

  function usage(main, r, c) {
    var box = el("section", "metric usage");
    var covered = c.last_30_days.cost_usd === 0 && c.last_30_days.api_equivalent_usd > 0;
    var grid = el("div", "stats");
    grid.appendChild(stat("Today", amount(c.today, covered)));
    grid.appendChild(stat("Last 30 days cost", amount(c.last_30_days, covered)));
    grid.appendChild(stat("Last 30 days tokens", tokens(c.last_30_days.tokens)));
    grid.appendChild(stat("Today tokens", tokens(c.today.tokens)));
    box.appendChild(grid);
    if (c.daily && c.daily.length) box.appendChild(chart(c.daily));
    if (c.top_model) box.appendChild(el("div", "top-model", "Top model: " + c.top_model));
    var notes = ["From local " + name(r.provider) + " logs."], s30 = c.last_30_days;
    if (covered && share(s30) <= 0.5) notes.push("Cost at API prices; your plan covers it.");
    if (share(s30) > 0.02) {
      notes.push(Math.round(share(s30) * 100) + "% of requests use models without a list price yet" +
        (share(s30) > 0.5 ? "." : ", left out of the $ figures."));
    }
    box.appendChild(el("div", "note", notes.join(" ")));
    main.appendChild(box);
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
    var right = el("div", "who");
    var account = v.glance && v.glance.accounts && v.glance.accounts[r.provider];
    if (account) right.appendChild(el("div", "account", account));
    right.appendChild(el("div", "plan", badge(r)));
    head.appendChild(right);
    main.appendChild(head);

    (r.windows || []).forEach(function (w) {
      var stale = w.stale && w.observed_at ? Math.max(60, (Date.now() - Date.parse(w.observed_at)) / 1000) : 0;
      main.appendChild(windowSection(title(w), w.used_pct, w.resets_in ? "Resets in " + human(seconds(w.resets_in)) : "", w.pace, stale));
    });
    if (r.spend_limit_usd > 0) {
      var s = windowSection("Extra usage", r.spend_pct, money(r.spend_usd) + " of " + money(r.spend_limit_usd) + " this month");
      main.appendChild(s);
    } else if (r.spend_usd > 0) {
      main.appendChild(el("p", "plain", "This month: " + money(r.spend_usd) + ", no limit known"));
    }
    if (r.balance_usd !== undefined && r.balance_usd !== null) {
      main.appendChild(el("p", "plain", money(r.balance_usd) + " credit left"));
    }
    var c = v.costs && v.costs[r.provider];
    if (c) usage(main, r, c);
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

  // A mark per level, each with words: the info mark was a faint middle
  // dot with no label, and read as a stray character.
  var MARKS_BY_LEVEL = { warn: "▲", notice: "●", info: "○" };
  var LEVEL_WORDS = { warn: "Needs attention", notice: "Worth a look", info: "For your information" };
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
      var mark = el("span", "finding-mark", MARKS_BY_LEVEL[f.level] || MARKS_BY_LEVEL.info);
      mark.title = LEVEL_WORDS[f.level] || LEVEL_WORDS.info;
      mark.setAttribute("role", "img");
      mark.setAttribute("aria-label", mark.title);
      item.appendChild(mark);
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
    var note = document.getElementById("note");
    note.hidden = !v.note;
    note.textContent = v.note || "";
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
  // Refresh asks the daemon's readers to poll now; re-reading alone would
  // return the readings the daemon already had.
  var refreshButton = document.getElementById("refresh");
  function refresh() {
    if (refreshButton.disabled) return;
    refreshButton.disabled = true;
    invoke("sources.refresh").then(function (v) {
      refreshButton.disabled = false;
      show(v);
    }, function (err) {
      refreshButton.disabled = false;
      state.view = state.view || {};
      state.view.error = "Not refreshed: " + ((err && err.message) || "refused");
      render();
    });
  }
  refreshButton.addEventListener("click", refresh);
  document.getElementById("close").addEventListener("click", function () { invoke("panel.close"); });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") invoke("panel.close");
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "r") {
      e.preventDefault();
      refresh();
    }
  });
})();
