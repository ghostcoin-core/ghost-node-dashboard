"use strict";

// Settings: the payout address, Restart node and the daily updates.
// All of this screen's markup is built here.
(function (D) {
  var h = D.h;
  var show = D.format.show;

  function el(id) {
    return document.getElementById(id);
  }

  function text(id, value) {
    el(id).textContent = value;
  }

  function payoutCard() {
    var c = D.card({ id: "payout-panel", title: "Payout address" });
    c.field("Rewards go to", "payout-current", { mono: true, wide: true });
    var input = h("input", {
      type: "text", id: "payout-input", class: "mono", maxlength: "42", spellcheck: false, autocomplete: "off",
      placeholder: "0x followed by 40 hex characters", pattern: "0x[0-9a-fA-F]{40}",
    });
    var save = h("button", { type: "submit", id: "payout-save", class: "action", text: "Save" });
    var form = h("form", { class: "controls", id: "payout-form" }, [
      h("label", { for: "payout-input", text: "New payout address (where rewards go)" }),
      h("div", { class: "control-row" }, [input, save]),
    ]);
    c.body.appendChild(form);
    c.body.appendChild(h("p", { class: "message", id: "payout-result", role: "status", hidden: true }));
    c.body.appendChild(h("p", { class: "note" }, [
      "A new payout address applies at once and is saved in the node's ",
      h("code", { text: "config.toml" }),
      ", so restarts keep it. Only the address is needed: never enter a private key or recovery words here.",
    ]));

    var busy = false;
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      if (busy) return;
      busy = true;
      save.disabled = true;
      showResult("payout-result", { ok: true, message: "Asking the node…" });
      D.postJSON("/api/mining/payout", { address: input.value.trim() })
        .then(function (res) {
          showResult("payout-result", res);
          if (res.saved) input.value = "";
          // The rewards screen starts counting for the new address.
          D.refresh();
        })
        .catch(function (err) {
          showResult("payout-result", { ok: false, error: err.message });
        })
        .then(function () {
          busy = false;
          save.disabled = false;
        });
    });
    return c;
  }

  function renderPayout(panel, s) {
    if (s.running) panel.setState(s.payoutAddress ? "Set" : "Not set", s.payoutAddress ? "ok" : "wait");
    text("payout-current", show(s.payoutAddress));
  }

  function showResult(id, res) {
    var box = el(id);
    var msg = res.ok ? res.message : res.error;
    if (res.command && res.command.length) msg += " (ran: " + res.command.join(" ") + ")";
    box.textContent = msg || "";
    box.hidden = !msg;
    box.className = res.ok ? "message" : "message error";
  }

  // Restart and updates.
  var serviceCardRef = null;
  var serviceBusy = false;

  function serviceCard() {
    var c = D.card({ id: "service-panel", title: "Restart and updates" });
    c.field("Daily updates", "updates-state");
    c.field("Last update run", "updates-last");
    c.field("Update service", "updates-service", { mono: true, wide: true });
    var buttons = [
      ["node-restart", "Restart node", "/api/node/restart", "Restarting the node and waiting for it to answer again…", false],
      ["updates-run", "Update now", "/api/updates/run", "Updating the node. This can take a few minutes…", false],
      ["updates-on", "Turn daily updates on", "/api/updates/on", "Turning daily updates on…", true],
      ["updates-off", "Turn daily updates off", "/api/updates/off", "Turning daily updates off…", true],
    ].map(function (b) {
      var button = h("button", { type: "button", id: b[0], class: "action", hidden: b[4], text: b[1] });
      button.addEventListener("click", function () {
        serviceAction(b[2], b[3]);
      });
      return button;
    });
    c.body.appendChild(h("div", { class: "control-row" }, buttons));
    c.body.appendChild(h("p", { class: "message", id: "service-result", role: "status", hidden: true }));
    c.body.appendChild(h("pre", { class: "output", id: "updates-output", hidden: true }));
    return c;
  }

  function setServiceBusy(on) {
    serviceBusy = on;
    ["node-restart", "updates-run", "updates-on", "updates-off"].forEach(function (id) {
      el(id).disabled = on;
    });
  }

  function showServiceResult(res) {
    showResult("service-result", res);
    var out = el("updates-output");
    out.textContent = res.output || "";
    out.hidden = !res.output;
  }

  function serviceAction(url, waiting) {
    if (serviceBusy) return;
    setServiceBusy(true);
    showServiceResult({ ok: true, message: waiting });
    D.postJSON(url)
      .then(function (res) {
        showServiceResult(res);
        if (res.updates) renderUpdates(serviceCardRef, res.updates);
      })
      .catch(function (err) {
        showServiceResult({ ok: false, error: err.message });
      })
      .then(function () {
        setServiceBusy(false);
        D.refresh();
      });
  }

  function renderUpdates(panel, s) {
    if (!s.available) panel.setState("Not available", "wait");
    else if (!s.installed) panel.setState("Updater not installed", "wait");
    else panel.setState("Ready", "ok");
    panel.setMessage(s.error ? "Restart and updates: " + s.error + "." : "");
    text("updates-state", s.on === true ? "On" : s.on === false ? "Off" : "–");
    text("updates-last", s.lastRunTime ? new Date(s.lastRunTime * 1000).toLocaleString() : s.lastRun || (s.installed ? "Never" : "–"));
    text("updates-service", show(s.service));
    el("node-restart").hidden = !s.available;
    el("updates-run").hidden = !s.available;
    el("updates-on").hidden = !s.installed || s.on !== false;
    el("updates-off").hidden = !s.installed || s.on !== true;
  }

  D.screen("settings", {
    title: "Settings",
    sentence: "Every change applies at once and is kept after restarts and updates.",
    mount: function (root) {
      var payout = payoutCard();
      serviceCardRef = serviceCard();
      root.appendChild(payout.root);
      root.appendChild(serviceCardRef.root);
      D.panel(payout, "/api/mining", renderPayout);
      D.panel(serviceCardRef, "/api/updates", renderUpdates, true);
    },
  });
})(GhostDash);
