// The tray panel. It talks to Go only through window.vitra.invoke; its
// grant lets it call glance.follow, coach.preset and panel.close, nothing
// else. It never sees the daemon's API token.
(function () {
  "use strict";

  function invoke(command, args) {
    return window.vitra.invoke(command, args || {}, "");
  }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  function level(pct) {
    if (pct >= 90) return "high";
    if (pct >= 70) return "medium";
    return "low";
  }

  function bar(label, pct, detail) {
    var row = el("div", "row");
    var head = el("div", "row-head");
    head.appendChild(el("span", "label", label));
    head.appendChild(el("span", "pct " + level(pct), Math.round(pct) + "%"));
    row.appendChild(head);
    var track = el("div", "track");
    var fill = el("div", "fill " + level(pct));
    fill.style.width = Math.max(0, Math.min(pct, 100)) + "%";
    track.appendChild(fill);
    row.appendChild(track);
    if (detail) row.appendChild(el("div", "detail muted", detail));
    return row;
  }

  // humanDuration words a Go duration ("146h45m0s") as "6d 2h".
  function humanDuration(s) {
    var m = /^(?:(\d+)h)?(?:(\d+)m)?(?:[\d.]+s)?$/.exec(s || "");
    if (!m) return s;
    var total = (parseInt(m[1] || "0", 10) * 60) + parseInt(m[2] || "0", 10);
    var d = Math.floor(total / 1440), h = Math.floor((total % 1440) / 60), min = total % 60;
    if (d > 0) return d + "d " + h + "h";
    if (h > 0) return h + "h " + min + "m";
    return min + "m";
  }

  function money(n) {
    return "$" + n.toFixed(n < 100 ? 2 : 0);
  }

  function plan(r) {
    var card = el("article", "plan");
    var head = el("div", "plan-head");
    head.appendChild(el("span", "name", r.display || r.provider));
    head.appendChild(el("span", "risk " + (r.overage_risk || "low"), r.overage_risk || ""));
    card.appendChild(head);
    (r.windows || []).forEach(function (w) {
      card.appendChild(bar(w.name, w.used_pct, w.resets_in ? "resets in " + humanDuration(w.resets_in) : ""));
    });
    if ((!r.windows || !r.windows.length) && r.spend_limit_usd > 0) {
      card.appendChild(bar("spend", r.spend_pct, money(r.spend_usd) + " of " + money(r.spend_limit_usd)));
    } else if ((!r.windows || !r.windows.length) && r.spend_usd > 0) {
      card.appendChild(el("div", "detail muted", money(r.spend_usd) + " this month, no limit known"));
    }
    if (r.balance_usd !== undefined && r.balance_usd !== null) {
      card.appendChild(el("div", "detail muted", money(r.balance_usd) + " credit left"));
    }
    return card;
  }

  function show(v) {
    var plans = document.getElementById("plans");
    var error = document.getElementById("error");
    plans.textContent = "";
    error.hidden = !v.error;
    error.textContent = v.error || "";
    var g = v.glance || {};
    document.getElementById("insight").textContent = (g.insight && g.insight.summary) || "";
    var head = g.plan_headroom || {};
    if (head.error) {
      error.hidden = false;
      error.textContent = head.hint || head.error;
    }
    (head.reports || []).forEach(function (r) { plans.appendChild(plan(r)); });
    if (v.coach) {
      var select = document.getElementById("preset");
      var custom = document.getElementById("preset-custom");
      if (v.coach.preset) {
        if (custom) custom.remove();
        select.value = v.coach.preset;
      } else {
        // Tuned away from every preset: say so rather than show one.
        if (!custom) {
          custom = el("option", "", "custom — tuned dial by dial");
          custom.id = "preset-custom";
          custom.disabled = true;
          select.insertBefore(custom, select.firstChild);
        }
        select.value = "";
        custom.selected = true;
      }
    }
    if (v.updated) {
      document.getElementById("updated").textContent = "updated " + new Date(v.updated).toLocaleTimeString();
    }
  }

  window.vitra.on("glance.update", show);
  invoke("glance.follow").then(show, function (err) {
    show({ error: (err && err.message) || "the daemon could not be read" });
  });

  document.getElementById("preset").addEventListener("change", function (e) {
    invoke("coach.preset", { preset: e.target.value }).then(show, function (err) {
      show({ error: "coach not changed: " + ((err && err.message) || "refused") });
    });
  });
  document.getElementById("close").addEventListener("click", function () {
    invoke("panel.close");
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") invoke("panel.close");
  });
})();
