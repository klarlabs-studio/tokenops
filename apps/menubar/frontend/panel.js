// The tray panel. It talks to Go only through window.vitra.invoke; its
// grant lets it read the glance, change the coach preset, answer
// boundaries, act on and hide findings, and close itself, nothing else.
// It never sees the daemon's API token.
(function () {
  "use strict";

  // Each provider's name, and whether logos/<id>.svg is its own logo
  // (logos/NOTICE.md), from TokenOps' provider registry (providers.js,
  // generated). A vendor without a logo shows two letters.
  var PROVIDERS = window.TOKENOPS_PROVIDERS || {};
  // open is the finding whose confirm row or hide choices are showing:
  // { key, action } or { key, hide: true }. busy is a finding being acted on.
  var state = { view: null, selected: null, refreshing: false, open: null, busy: null };
  // more is whether the coach's information findings are unfolded.
  state.more = false;
  try {
    state.selected = localStorage.getItem("tokenops.tab");
    state.more = localStorage.getItem("tokenops.coach.more") === "1";
  } catch (e) { /* a private window */ }

  function invoke(command, args) { return window.vitra.invoke(command, args || {}, ""); }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = text;
    return e;
  }

  function name(p) {
    var known = PROVIDERS[p];
    return known ? known.name : (p ? p.charAt(0).toUpperCase() + p.slice(1) : "?");
  }

  function mark(p, cls) {
    var m = el("span", "mark " + (cls || ""));
    if (PROVIDERS[p] && PROVIDERS[p].logo) {
      var img = el("img");
      img.src = "logos/" + p + ".svg";
      img.alt = "";
      m.appendChild(img);
    } else {
      m.classList.add("letters");
      m.textContent = name(p).slice(0, 2);
    }
    return m;
  }

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

  function tabs(reports, v) {
    var nav = document.getElementById("tabs");
    nav.textContent = "";
    reports.forEach(function (r) {
      var b = el("button", "tab" + (r.provider === state.selected ? " active" : ""));
      b.type = "button";
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", r.provider === state.selected ? "true" : "false");
      b.appendChild(mark(r.provider));
      var label = el("span", "tab-name", name(r.provider));
      // A warning about this plan's windows shows in its tab, so it is
      // marked here for when another plan is selected.
      if (planFindings(v, r.provider).length) {
        var alert = el("span", "tab-alert", MARKS_BY_LEVEL.warn);
        alert.setAttribute("role", "img");
        alert.setAttribute("aria-label", LEVEL_WORDS.warn);
        alert.title = LEVEL_WORDS.warn;
        label.appendChild(alert);
      }
      b.appendChild(label);
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

  // The operator's own budgets on this provider's bill (budget set
  // --provider), each window as a meter: their limit, apart from any cap
  // the provider sets on the account.
  function providerBudgets(main, r, v) {
    var all = (v.glance && v.glance.spend_budgets) || [];
    all.filter(function (b) { return b.provider === r.provider && b.limit > 0; }).forEach(function (b) {
      var amt = b.basis === "tokens"
        ? tokens(b.used) + " of " + tokens(b.limit) + " tokens spent"
        : money(b.used) + " of " + money(b.limit) + " spent";
      var s = windowSection("Budget " + b.budget + " · " + b.window, b.used_pct, amt);
      var mode = "Soft: notifies";
      if (b.rule && b.enforced) mode = "Hard: " + (b.require === "deny" ? "refuses" : "asks") + " at the limit";
      else if (b.rule) mode = "Hard, in shadow: stops nothing yet";
      s.appendChild(pair("metric-line", mode, ""));
      main.appendChild(s);
    });
  }

  // Over the last week: how full the plan's longest window got on average
  // (are the seats used?) and how much of the usage the plan carried
  // rather than an API key or a gateway.
  function planUse(main, r, v) {
    var all = (v.glance && v.glance.plan_use && v.glance.plan_use.providers) || [];
    var u = all.filter(function (p) { return p.provider === r.provider; })[0];
    if (!u || (u.utilization_pct == null && u.coverage_pct == null)) return;
    var box = el("section", "metric usage");
    var grid = el("div", "stats");
    if (u.utilization_pct != null) {
      grid.appendChild(stat("Plan used, 7 days", Math.round(u.utilization_pct) + "% of " + u.utilization_window));
    }
    if (u.coverage_pct != null) grid.appendChild(stat("On the plan, 7 days", Math.round(u.coverage_pct) + "% of tokens"));
    box.appendChild(grid);
    main.appendChild(box);
  }

  function detail(r, v) {
    var main = document.getElementById("detail");
    main.textContent = "";
    var head = el("header", "head");
    var left = el("div");
    var h = el("h2");
    h.appendChild(mark(r.provider, "mark-large"));
    h.appendChild(document.createTextNode(name(r.provider)));
    left.appendChild(h);
    var age = v.updated ? Math.round((Date.now() - new Date(v.updated)) / 60000) : 0;
    left.appendChild(el("div", "muted", age < 1 ? "Updated just now" : "Updated " + age + "m ago"));
    head.appendChild(left);
    var right = el("div", "who");
    var account = v.glance && v.glance.accounts && v.glance.accounts[r.provider];
    if (account) right.appendChild(el("div", "account", account));
    right.appendChild(el("div", "plan", badge(r)));
    head.appendChild(right);
    main.appendChild(head);

    // A warning about the plan as a whole leads the pane; one about a
    // window sits under that window's bar.
    var mine = planFindings(v, r.provider);
    var known = {};
    (r.windows || []).forEach(function (w) { known[w.name] = true; });
    var loose = mine.filter(function (f) { return !f.window || !known[f.window]; });
    if (loose.length) main.appendChild(findingList("plan-findings", loose));
    (r.windows || []).forEach(function (w) {
      var stale = w.stale && w.observed_at ? Math.max(60, (Date.now() - Date.parse(w.observed_at)) / 1000) : 0;
      var resets = w.not_started ? "Starts at your next request" : w.resets_in ? "Resets in " + human(seconds(w.resets_in)) : "";
      var s = windowSection(title(w), w.used_pct, resets, w.pace, stale);
      var about = mine.filter(function (f) { return f.window === w.name; });
      if (about.length) s.appendChild(findingList("window-findings", about));
      main.appendChild(s);
    });
    // An account-wide figure is every member's spend together, not the
    // operator's own: it is titled and labelled as the account's.
    var whole = r.spend_scope === "account";
    var whose = whole && r.spend_scope_label ? " — " + r.spend_scope_label : "";
    if (r.spend_limit_usd > 0) {
      var s = windowSection(whole ? "Account spend" : "Extra usage", r.spend_pct,
        money(r.spend_usd) + " of " + money(r.spend_limit_usd) + " this month" + whose);
      main.appendChild(s);
    } else if (r.spend_usd > 0) {
      main.appendChild(el("p", "plain", (whole ? "Account this month: " : "This month: ") + money(r.spend_usd) + ", no limit known" + whose));
    }
    if (r.balance_usd !== undefined && r.balance_usd !== null) {
      main.appendChild(el("p", "plain", money(r.balance_usd) + " credit left"));
    }
    providerBudgets(main, r, v);
    planUse(main, r, v);
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

  function plansShown(v) {
    var shown = {};
    (((v.glance && v.glance.plan_headroom) || {}).reports || []).forEach(function (r) { shown[r.provider] = true; });
    return shown;
  }

  // A plan's own warnings: a quota finding about one of the plans the
  // tabs show belongs in that plan's tab, beside the window it concerns.
  function planFindings(v, provider) {
    var all = (v && v.findings && v.findings.findings) || [];
    if (!plansShown(v)[provider]) return [];
    return all.filter(function (f) { return f.kind === "quota" && f.provider === provider; });
  }

  function findingItem(f) {
    var item = el("div", "finding " + f.level);
    var mark = el("span", "finding-mark", MARKS_BY_LEVEL[f.level] || MARKS_BY_LEVEL.info);
    mark.title = LEVEL_WORDS[f.level] || LEVEL_WORDS.info;
    mark.setAttribute("role", "img");
    mark.setAttribute("aria-label", mark.title);
    item.appendChild(mark);
    var body = el("div", "finding-body");
    body.appendChild(el("div", "finding-title", f.title));
    if (f.evidence) body.appendChild(withCode(el("div", "note"), f.evidence));
    // A finding with buttons does what its command would; the command
    // stays for the CLI and MCP, where nothing can be clicked.
    if (f.action && !(f.actions && f.actions.length)) body.appendChild(withCode(el("div", "finding-action"), f.action));
    if (f.key) findingControls(body, f);
    item.appendChild(body);
    return item;
  }

  function findingList(cls, list) {
    var box = el("div", "findings " + cls);
    list.forEach(function (f) { box.appendChild(findingItem(f)); });
    return box;
  }

  // What the coach did on its own, from the daemon's coach report:
  // today's when it did anything today, else the last week's, said as
  // such. Nothing is said for a daemon that does not report it.
  function activityLine(v) {
    var periods = (v.coach && v.coach.activity) || [];
    if (!periods.length) return null;
    var today = periods.filter(function (a) { return a.period === "today"; })[0];
    var week = periods.filter(function (a) { return a.period === "7d"; })[0];
    if (today && today.summary) return "Today: " + today.summary + ".";
    if (week && week.summary) return "Last 7 days: " + week.summary + ".";
    return "Nothing done on its own in the last 7 days.";
  }

  // The coach's findings, ranked by the daemon. What needs attention or
  // is worth a look stays open; information folds behind "N more". A
  // plan's window warnings are in its tab instead.
  function coachFindings(main, v) {
    var report = v.findings;
    if (!report || !report.findings) return;
    var shown = plansShown(v);
    var listed = report.findings.filter(function (f) { return !(f.kind === "quota" && shown[f.provider]); });
    var urgent = listed.filter(function (f) { return f.level === "warn" || f.level === "notice"; });
    var info = listed.filter(function (f) { return !(f.level === "warn" || f.level === "notice"); });
    var box = el("section", "metric coach");
    var head = el("h3", null, "Coach");
    var count;
    if (urgent.length) count = urgent.length + " to look at";
    else count = info.length ? "nothing urgent" : "nothing stands out";
    head.appendChild(el("span", "count", count));
    box.appendChild(head);
    var did = activityLine(v);
    if (did) box.appendChild(el("p", "coach-did", did));
    if (urgent.length) box.appendChild(findingList("coach-urgent", urgent));
    if (info.length) {
      var more = button("more", (state.more ? "Fewer" : info.length + " more"),
        (state.more ? "Fold " : "Show ") + info.length + " more for your information");
      more.id = "coach-more";
      more.setAttribute("aria-expanded", String(state.more));
      more.setAttribute("aria-controls", "coach-info");
      more.addEventListener("click", function () {
        state.more = !state.more;
        try { localStorage.setItem("tokenops.coach.more", state.more ? "1" : "0"); } catch (e) { /* ignore */ }
        render();
        focus("#coach-more");
      });
      box.appendChild(more);
      var folded = findingList("coach-info", info);
      folded.id = "coach-info";
      folded.hidden = !state.more;
      box.appendChild(folded);
    }
    if (report.hidden > 0) {
      var hidden = el("div", "findings-hidden", report.hidden + " hidden · ");
      var showAll = button("link", "Show", "Show the findings you hid");
      showAll.addEventListener("click", function () { act("finding.dismiss", { key: "", for: "none" }, null); });
      hidden.appendChild(showAll);
      box.appendChild(hidden);
    }
    if (report.sessions_read_at) {
      var mins = Math.round((Date.now() - new Date(report.sessions_read_at)) / 60000);
      box.appendChild(el("div", "note", "Sessions read " + (mins < 60 ? mins + "m" : Math.round(mins / 60) + "h") + " ago."));
    }
    main.appendChild(box);
  }

  function button(cls, text, label) {
    var b = el("button", "fbtn " + cls, text);
    b.type = "button";
    if (label) b.setAttribute("aria-label", label);
    return b;
  }

  function isOpen(f, what) {
    var o = state.open;
    return !!o && o.key === f.key && (what === "hide" ? !!o.hide : o.action === what);
  }

  // A finding's actions, the first as the primary button, and its hide
  // choices. An action with a confirm sentence opens a row inside the
  // finding that says what will change; nothing runs until Confirm.
  function findingControls(body, f) {
    var busy = state.busy === f.key;
    var row = el("div", "finding-controls");
    (f.actions || []).forEach(function (a, i) {
      var b = button(i === 0 ? "primary" : "quiet", a.label, a.label + ": " + f.title);
      b.disabled = busy;
      if (a.confirm) b.setAttribute("aria-expanded", String(isOpen(f, a.id)));
      b.addEventListener("click", function () {
        if (a.confirm) {
          state.open = isOpen(f, a.id) ? null : { key: f.key, action: a.id };
          render();
          focus(".finding-confirm .primary");
          return;
        }
        act("finding.act", { key: f.key, action: a.id }, f.key);
      });
      row.appendChild(b);
    });
    var hide = button("quiet hide", "Hide…", "Hide: " + f.title);
    hide.disabled = busy;
    hide.setAttribute("aria-expanded", String(isOpen(f, "hide")));
    hide.addEventListener("click", function () {
      state.open = isOpen(f, "hide") ? null : { key: f.key, hide: true };
      render();
      focus(".finding-hide .quiet");
    });
    row.appendChild(hide);
    body.appendChild(row);

    var chosen = (f.actions || []).filter(function (a) { return a.confirm && isOpen(f, a.id); })[0];
    if (chosen) {
      var confirm = el("div", "finding-confirm");
      confirm.setAttribute("role", "group");
      confirm.setAttribute("aria-label", "Confirm " + chosen.label);
      confirm.appendChild(withCode(el("div", "confirm-text"), chosen.confirm));
      var yes = button("primary", "Confirm", "Confirm " + chosen.label);
      yes.disabled = busy;
      yes.addEventListener("click", function () { act("finding.act", { key: f.key, action: chosen.id }, f.key); });
      var no = button("quiet", "Cancel");
      no.addEventListener("click", function () { state.open = null; render(); });
      var buttons = el("div", "finding-controls");
      buttons.appendChild(yes);
      buttons.appendChild(no);
      confirm.appendChild(buttons);
      body.appendChild(confirm);
    }
    if (isOpen(f, "hide")) {
      var choices = el("div", "finding-controls finding-hide");
      choices.setAttribute("role", "group");
      choices.setAttribute("aria-label", "Hide " + f.title);
      [["day", "Hide for a day"], ["week", "Hide for a week"]].forEach(function (c) {
        var b = button("quiet", c[1]);
        b.disabled = busy;
        b.addEventListener("click", function () { act("finding.dismiss", { key: f.key, for: c[0] }, f.key); });
        choices.appendChild(b);
      });
      var cancel = button("quiet", "Cancel");
      cancel.addEventListener("click", function () { state.open = null; render(); });
      choices.appendChild(cancel);
      body.appendChild(choices);
    }
  }

  function focus(selector) {
    var e = document.querySelector(selector);
    if (e) e.focus();
  }

  // act runs a finding command; Go leaves what happened in the note.
  function act(command, args, key) {
    state.busy = key;
    render();
    invoke(command, args).then(function (v) {
      state.busy = null;
      state.open = null;
      show(v);
    }, function (err) {
      failed(command, err);
      state.busy = null;
      state.view = state.view || {};
      state.view.error = command === "finding.act"
        ? "That didn't go through, and nothing was changed. Try again in a moment."
        : "The findings weren't hidden or shown. Try again in a moment.";
      render();
    });
  }

  // Decisions a boundary is waiting on (ADR 0014). Deny goes through the
  // daemon and never signs. Approve needs a person: with a presence key
  // enrolled (ADR 0015) it raises the Touch ID, Apple Watch or password
  // dialog and posts the signed answer; without one it opens a terminal
  // that asks. Go decides which, and says in the note what happened.
  function boundaryWaits(main, v) {
    var pending = (v.boundaries && v.boundaries.pending) || [];
    if (!pending.length) return;
    var presence = (v.boundaries && v.boundaries.presence) || {};
    var inPerson = !!presence.key_id && presence.state !== "held";
    var box = el("section", "metric waits");
    var head = el("h3", null, "Waiting on you");
    head.appendChild(el("span", "count", String(pending.length)));
    box.appendChild(head);
    pending.forEach(function (p) {
      var item = el("div", "wait");
      item.appendChild(el("div", "finding-title", p.rule));
      item.appendChild(el("div", "note", p.crossed));
      item.appendChild(el("div", "note", (p.harness || "tokenops") + " · needs " + p.require));
      var actions = el("div", "wait-actions");
      if (p.routed) {
        // An organisation rule: its role answers in the team plane, not here.
        item.appendChild(el("div", "note", p.approve));
      } else {
        var approve = el("button", "wait-approve", inPerson ? "Approve" : "Approve…");
        approve.type = "button";
        approve.title = inPerson
          ? "Asks for Touch ID, your Apple Watch or your password, then approves this step"
          : "Opens a terminal that shows this and asks you to confirm";
        approve.addEventListener("click", function () {
          approve.disabled = true;
          approve.textContent = inPerson ? "Approving…" : "Opening…";
          answer("boundary.approve", p.id);
        });
        actions.appendChild(approve);
      }
      var deny = el("button", "wait-deny", "Deny");
      deny.type = "button";
      deny.addEventListener("click", function () { answer("boundary.deny", p.id); });
      actions.appendChild(deny);
      item.appendChild(actions);
      box.appendChild(item);
    });
    main.insertBefore(box, main.firstChild);
  }

  function answer(command, id) {
    invoke(command, { id: id }).then(show, function (err) {
      failed(command, err);
      state.view = state.view || {};
      state.view.error = command === "boundary.approve"
        ? "No terminal opened. Approve it yourself: tokenops approve " + id + " --confirm"
        : "The denial didn't go through. Try again in a moment.";
      render();
    });
  }

  // While a read is in progress the panel keeps what it shows and says,
  // after a second, how long it has been fetching.
  function loading() {
    var v = state.view, box = document.getElementById("loading");
    var since = v ? (v.loading_since ? Date.parse(v.loading_since) : 0) : Date.now() - 1000;
    var secs = since ? Math.floor((Date.now() - since) / 1000) : 0;
    box.hidden = !since || secs < 1;
    if (box.hidden) return;
    var text = "Fetching your usage…";
    if (secs >= 3) text = "Fetching your usage… " + secs + "s";
    if (secs >= 15) text = "This is taking a while: " + secs + "s so far." +
      (v && v.glance ? " What you see is from before." : "");
    document.getElementById("loading-text").textContent = text;
  }
  setInterval(loading, 1000);

  function refreshButton() {
    var busy = state.refreshing || !!(state.view && state.view.refreshing);
    var b = document.getElementById("refresh");
    b.classList.toggle("busy", busy);
    b.setAttribute("aria-busy", busy ? "true" : "false");
    document.getElementById("refresh-label").textContent = busy ? "Refreshing…" : "Refresh";
  }

  function render() {
    var v = state.view || {};
    loading();
    refreshButton();
    var error = document.getElementById("error");
    var head = (v.glance && v.glance.plan_headroom) || {};
    var msg = v.error || (head.error ? (head.hint || head.error) : "");
    error.hidden = !msg;
    error.textContent = msg;
    error.classList.toggle("waiting", !!(v.error && v.waiting));
    var note = document.getElementById("note");
    note.hidden = !v.note;
    note.textContent = v.note || "";
    note.classList.toggle("warn", !!v.note_warn);
    var reports = (head.reports || []).slice().sort(function (a, b) { return busiest(b) - busiest(a); });
    if (!reports.some(function (r) { return r.provider === state.selected; }) && reports.length) {
      state.selected = reports[0].provider;
    }
    tabs(reports, v);
    var sel = reports.filter(function (r) { return r.provider === state.selected; })[0];
    if (sel) detail(sel, v); else document.getElementById("detail").textContent = "";
    coachFindings(document.getElementById("detail"), v);
    boundaryWaits(document.getElementById("detail"), v);
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
    presetHelp(v, select.value);
  }

  // presetHelp says in one line what the chosen preset does, from the
  // daemon's preset definitions (GET /api/coach presets).
  function presetHelp(v, chosen) {
    var help = document.getElementById("preset-help");
    var presets = (v.coach && v.coach.presets) || [];
    var p = presets.filter(function (x) { return x.name === chosen; })[0];
    var text = "";
    if (p) text = p.summary.charAt(0).toUpperCase() + p.summary.slice(1) + ".";
    else if (v.coach && !v.coach.preset && presets.length) text = "Tuned by hand: `tokenops coach` shows each power.";
    help.textContent = "";
    withCode(help, text);
    help.hidden = !text;
  }

  function show(v) { state.view = v; render(); }

  window.vitra.on("glance.update", show);
  // Rejections are logged; the panel says what happened in words.
  function failed(what, err) {
    if (window.console) console.error(what, err);
  }
  render();
  invoke("glance.follow").then(show, function (err) {
    failed("glance.follow", err);
    show({ error: "Your usage couldn't be fetched just now. TokenOps will try again in a minute." });
  });
  document.getElementById("preset").addEventListener("change", function (e) {
    presetHelp(state.view || {}, e.target.value);
    invoke("coach.preset", { preset: e.target.value }).then(show, function (err) {
      failed("coach.preset", err);
      state.view = state.view || {};
      state.view.error = "The coach setting wasn't changed. Try again in a moment.";
      render();
    });
  });
  // Refresh asks the daemon's readers to poll now; re-reading alone would
  // return the readings the daemon already had.
  function refresh() {
    if (state.refreshing || (state.view && state.view.refreshing)) return;
    state.refreshing = true;
    refreshButton();
    invoke("sources.refresh").then(function (v) {
      state.refreshing = false;
      show(v);
    }, function (err) {
      failed("sources.refresh", err);
      state.refreshing = false;
      state.view = state.view || {};
      state.view.error = "Refresh didn't go through. TokenOps refreshes on its own every minute.";
      render();
    });
  }
  document.getElementById("refresh").addEventListener("click", refresh);
  document.getElementById("close").addEventListener("click", function () { invoke("panel.close"); });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") invoke("panel.close");
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "r") {
      e.preventDefault();
      refresh();
    }
  });
})();
