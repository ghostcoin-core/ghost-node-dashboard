"use strict";

// Settings: mining, the payout address, the daily updates and the node.
// All of this screen's markup is built here; its looks are in settings.css,
// which the screen adds to the page once, when it mounts.
(function (D) {
  var h = D.h;
  var f = D.format;
  var show = f.show;

  var STYLESHEET = "/static/screens/settings.css";
  var SVG_NS = "http://www.w3.org/2000/svg";
  var ADDRESS = /^0x[0-9a-fA-F]{40}$/;
  var NOT_ADDRESS = "That is not an address: it should start with 0x followed by 40 letters and numbers.";
  var NOT_RUNNING = "Start the node to change mining.";
  var NOT_AVAILABLE = "Updates and restart are not available on this computer.";
  var RESTART_WAIT = "Restarting the node and waiting for it to answer again…";
  var RUN_WAIT = "Checking for an update. This can take a few minutes…";

  var el = {};
  var mining = { running: false, dragging: false, busy: false };
  var payout = { typed: false, busy: false };
  var service = { busy: false, available: false, installed: false, on: null, confirming: false };

  function loadStyles() {
    var links = document.head.querySelectorAll('link[rel="stylesheet"]');
    for (var i = 0; i < links.length; i++) {
      if (links[i].getAttribute("href") === STYLESHEET) return;
    }
    document.head.appendChild(h("link", { rel: "stylesheet", href: STYLESHEET }));
  }

  // showResult writes an action's answer into its status line.
  function showResult(box, res) {
    var msg = res.ok ? res.message : res.error || res.message;
    if (res.command && res.command.length) msg += " (ran: " + res.command.join(" ") + ")";
    box.textContent = msg || "";
    box.hidden = !msg;
    box.className = res.ok ? "message st-status" : "message st-status error";
  }

  function statusLine(id) {
    return h("p", { class: "message st-status", id: id, role: "status", hidden: true });
  }

  function note(id) {
    return h("p", { class: "st-note", id: id, hidden: true });
  }

  function setNote(node, text) {
    node.textContent = text || "";
    node.hidden = !text;
  }

  // A switch is a real button; settings.css draws the track and the knob
  // from aria-checked.
  function toggle(id, label) {
    return h("button", { type: "button", id: id, class: "st-switch", role: "switch", "aria-checked": "false", "aria-label": label });
  }

  function setChecked(sw, on) {
    sw.setAttribute("aria-checked", on ? "true" : "false");
  }

  function card(id, title, description) {
    var titleId = id + "-title";
    var head = [h("h2", { id: titleId, text: title })];
    if (description) head.push(h("p", { class: "st-desc", text: description }));
    return h("section", { class: "card st-card", id: id, "aria-labelledby": titleId }, [h("div", { class: "st-head" }, head)]);
  }

  function lockIcon() {
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("width", "16");
    svg.setAttribute("height", "16");
    svg.setAttribute("fill", "none");
    svg.setAttribute("stroke", "#5F5D73");
    svg.setAttribute("stroke-width", "2");
    svg.setAttribute("stroke-linecap", "round");
    svg.setAttribute("stroke-linejoin", "round");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    svg.setAttribute("class", "st-lock");
    var rect = document.createElementNS(SVG_NS, "rect");
    rect.setAttribute("x", "5");
    rect.setAttribute("y", "11");
    rect.setAttribute("width", "14");
    rect.setAttribute("height", "10");
    rect.setAttribute("rx", "2");
    var path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("d", "M8 11V8a4 4 0 0 1 8 0v3");
    svg.appendChild(rect);
    svg.appendChild(path);
    return svg;
  }

  // Formatting.

  function pad(n) {
    return n < 10 ? "0" + n : String(n);
  }

  // lastChecked is the line under "Update every day".
  function lastChecked(s) {
    if (typeof s.lastRunTime === "number") {
      var t = new Date(s.lastRunTime * 1000);
      var now = new Date();
      var clock = pad(t.getHours()) + ":" + pad(t.getMinutes());
      var day = t.toDateString() === now.toDateString() ? "today at " + clock : t.toLocaleDateString() + " " + clock;
      return "Last checked " + day + " · up to date";
    }
    if (s.lastRun) return "Last checked " + s.lastRun;
    return "Not checked yet";
  }

  function syncText(s) {
    if (s.syncing === false) return "In sync";
    if (s.syncing !== true) return "–";
    if (typeof s.currentBlock !== "number" || typeof s.highestBlock !== "number" || s.highestBlock === 0) return "Syncing";
    return "Syncing, " + Math.min(100, (s.currentBlock / s.highestBlock) * 100).toFixed(1) + " %";
  }

  function uptimeText(s) {
    if (typeof s.uptimeSeconds !== "number") return "–";
    return (s.uptimeFrom === "first answer" ? "at least " : "") + f.duration(s.uptimeSeconds);
  }

  // versionText shows "v1.4.2" from "Geth/v1.4.2-stable/linux-amd64/go1.21".
  function versionText(v) {
    if (!v) return "–";
    var m = /\/(v\d+(?:\.\d+)*)/.exec(v);
    return m ? m[1] : v;
  }

  // Mining.

  function miningCard() {
    var root = card("st-mining", "Mining", "More cores win more often but make the computer warmer and louder.");
    el.coresCount = h("span", { class: "mono", id: "st-cores-count", text: "–" });
    el.cores = h("input", { type: "range", id: "st-cores", class: "st-range", min: "1", max: "1", step: "1", value: "1", disabled: true });
    el.coresMax = h("span", { id: "st-cores-max", text: "– · fastest" });
    el.mine = toggle("st-mine", "Mine");
    el.mine.disabled = true;
    el.miningNote = note("st-mining-note");
    el.miningStatus = statusLine("st-mining-status");

    root.appendChild(h("label", { for: "st-cores", class: "st-label-row" }, [h("span", { text: "Cores" }), el.coresCount]));
    root.appendChild(el.cores);
    root.appendChild(h("div", { class: "st-scale" }, [h("span", { text: "1 · quietest" }), el.coresMax]));
    root.appendChild(h("div", { class: "st-divider" }, [
      h("div", {}, [
        h("p", { class: "st-strong", text: "Mine" }),
        h("p", { class: "st-desc", text: "Turned off, the node keeps syncing but earns nothing." }),
      ]),
      el.mine,
    ]));
    root.appendChild(el.miningNote);
    root.appendChild(el.miningStatus);

    el.cores.addEventListener("input", function () {
      mining.dragging = true;
      showCores(Number(el.cores.value), Number(el.cores.max));
    });
    el.cores.addEventListener("change", function () {
      mining.dragging = false;
      miningAction("/api/mining/cores", { cores: Number(el.cores.value) }, "Changing the cores…");
    });
    el.mine.addEventListener("click", function () {
      var on = el.mine.getAttribute("aria-checked") === "true";
      if (on) miningAction("/api/mining/pause", null, "Turning mining off…");
      else miningAction("/api/mining/resume", null, "Turning mining on…");
    });
    return root;
  }

  function showCores(n, max) {
    el.coresCount.textContent = n + " of " + max;
    el.cores.setAttribute("aria-valuetext", n + " of " + max + " cores");
  }

  function setMiningDisabled(off) {
    el.cores.disabled = off;
    el.mine.disabled = off;
  }

  function miningAction(url, body, waiting) {
    if (mining.busy) return;
    mining.busy = true;
    setMiningDisabled(true);
    showResult(el.miningStatus, { ok: true, message: waiting });
    D.postJSON(url, body)
      .then(function (res) {
        showResult(el.miningStatus, res);
        if (res.ok && typeof res.paused === "boolean") setChecked(el.mine, !res.paused);
      })
      .catch(function (err) {
        showResult(el.miningStatus, { ok: false, error: err.message });
      })
      .then(function () {
        mining.busy = false;
        setMiningDisabled(!mining.running);
        D.refresh();
      });
  }

  function renderMining(s) {
    mining.running = !!s.running;
    var max = Math.max(1, s.logicalCores || 1);
    var used = Math.min(max, Math.max(1, s.coresUsed || max));
    el.cores.max = String(max);
    el.coresMax.textContent = max + " · fastest";
    if (!mining.dragging && !mining.busy) {
      el.cores.value = String(used);
      showCores(used, max);
    }
    if (!mining.busy) {
      setChecked(el.mine, !s.paused);
      setMiningDisabled(!mining.running);
    }
    setNote(el.miningNote, mining.running ? "" : NOT_RUNNING);
    renderPayout(s);
  }

  // Payout address.

  function payoutCard() {
    var root = card("st-payout", "Payout address", "Where block rewards go.");
    el.payout = h("input", {
      type: "text", id: "payout-input", class: "mono st-input", maxlength: "42", spellcheck: false, autocomplete: "off",
      placeholder: "0x followed by 40 letters and numbers",
    });
    el.save = h("button", { type: "submit", id: "st-payout-save", class: "st-save", text: "Save" });
    el.payoutStatus = statusLine("st-payout-status");
    var form = h("form", { id: "st-payout-form", class: "st-form", novalidate: true }, [
      h("label", { for: "payout-input", class: "st-label", text: "Address" }),
      el.payout,
      h("div", { class: "st-row st-payout-row" }, [
        h("p", { class: "st-lock-note" }, [lockIcon(), h("span", { text: "Never enter a private key or recovery words." })]),
        el.save,
      ]),
    ]);
    root.appendChild(form);
    root.appendChild(el.payoutStatus);

    el.payout.addEventListener("input", function () {
      payout.typed = true;
    });
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      if (payout.busy) return;
      var address = el.payout.value.trim();
      if (!ADDRESS.test(address)) {
        showResult(el.payoutStatus, { ok: false, error: NOT_ADDRESS });
        el.payout.focus();
        return;
      }
      payout.busy = true;
      el.save.disabled = true;
      showResult(el.payoutStatus, { ok: true, message: "Saving the address…" });
      D.postJSON("/api/mining/payout", { address: address })
        .then(function (res) {
          showResult(el.payoutStatus, res);
          if (res.ok) {
            el.payout.value = res.payoutAddress || address;
            payout.typed = false;
          }
          // The rewards screen starts counting for the new address.
          D.refresh();
        })
        .catch(function (err) {
          showResult(el.payoutStatus, { ok: false, error: err.message });
        })
        .then(function () {
          payout.busy = false;
          el.save.disabled = false;
        });
    });
    return root;
  }

  function renderPayout(s) {
    if (payout.typed || payout.busy || !s.payoutAddress) return;
    el.payout.value = s.payoutAddress;
  }

  // Updates.

  function updatesCard() {
    var root = card("st-updates", "Updates");
    el.updatesSwitch = toggle("updates-on", "Update every day");
    el.updatesSwitch.disabled = true;
    el.updatesLast = h("p", { class: "st-desc", id: "st-updates-last", text: "Not checked yet" });
    el.updatesRun = h("button", { type: "button", id: "updates-run", class: "st-button", disabled: true, text: "Check for an update now" });
    el.updatesNote = note("st-updates-note");
    el.updatesStatus = statusLine("st-updates-status");
    el.updatesOutput = h("pre", { id: "updates-output", class: "st-output", hidden: true });

    root.appendChild(h("div", { class: "st-row st-switch-row" }, [
      h("div", {}, [h("p", { class: "st-strong", text: "Update every day" }), el.updatesLast]),
      el.updatesSwitch,
    ]));
    root.appendChild(h("div", { class: "st-actions" }, [el.updatesRun]));
    root.appendChild(el.updatesNote);
    root.appendChild(el.updatesStatus);
    root.appendChild(el.updatesOutput);

    el.updatesSwitch.addEventListener("click", function () {
      // The switch's id is the action a click performs.
      if (el.updatesSwitch.id === "updates-off") serviceAction("/api/updates/off", "Turning daily updates off…", el.updatesStatus);
      else serviceAction("/api/updates/on", "Turning daily updates on…", el.updatesStatus);
    });
    el.updatesRun.addEventListener("click", function () {
      serviceAction("/api/updates/run", RUN_WAIT, el.updatesStatus);
    });
    return root;
  }

  function renderUpdates(s) {
    service.available = !!s.available;
    service.installed = !!s.installed;
    service.on = s.on === true ? true : s.on === false ? false : null;
    setChecked(el.updatesSwitch, service.on === true);
    el.updatesSwitch.id = service.on === true ? "updates-off" : "updates-on";
    el.updatesLast.textContent = lastChecked(s);
    if (!service.available) setNote(el.updatesNote, s.error || NOT_AVAILABLE);
    else if (s.error) setNote(el.updatesNote, s.error);
    else if (!service.installed) setNote(el.updatesNote, "The daily update service is not installed on this computer.");
    else setNote(el.updatesNote, "");
    setNote(el.restartNote, service.available ? "" : s.error || NOT_AVAILABLE);
    if (!service.available && service.confirming) closeConfirm(false);
    setServiceDisabled(service.busy);
  }

  // Node.

  function nodeCard() {
    var root = h("section", { class: "card st-card", id: "st-node", "aria-labelledby": "st-node-title" });
    el.restart = h("button", { type: "button", id: "node-restart", class: "st-button", disabled: true, text: "Restart node" });
    el.restartNow = h("button", { type: "button", id: "st-restart-now", class: "st-save", text: "Restart now" });
    el.restartCancel = h("button", { type: "button", id: "st-restart-cancel", class: "st-button", text: "Cancel" });
    el.confirm = h("div", { id: "st-restart-confirm", class: "st-confirm", "aria-live": "polite" }, [
      h("p", { id: "st-restart-text", text: "" }),
      h("div", { class: "st-confirm-buttons", id: "st-restart-buttons", hidden: true }, [el.restartNow, el.restartCancel]),
    ]);
    el.restartNote = note("st-restart-note");
    el.nodeStatus = statusLine("st-node-status");

    root.appendChild(h("div", { class: "st-node-head" }, [
      h("div", { class: "st-head" }, [
        h("h2", { id: "st-node-title", text: "Node" }),
        h("p", { class: "st-desc", text: "Restarting takes about a minute." }),
      ]),
      el.restart,
    ]));
    root.appendChild(el.confirm);
    root.appendChild(el.restartNote);
    root.appendChild(el.nodeStatus);

    var list = h("dl", { class: "st-details" });
    el.details = {};
    [["status", "Status"], ["sync", "Sync"], ["up", "Up for"], ["peers", "Peers"], ["version", "Version"], ["chain", "Chain ID"]].forEach(function (d) {
      el.details[d[0]] = h("dd", { class: "mono", id: "st-node-" + d[0], text: "–" });
      list.appendChild(h("div", {}, [h("dt", { text: d[1] }), el.details[d[0]]]));
    });
    root.appendChild(list);

    el.restart.addEventListener("click", function () {
      if (service.busy || !service.available) return;
      openConfirm();
    });
    el.restartCancel.addEventListener("click", function () {
      closeConfirm(true);
    });
    el.restartNow.addEventListener("click", function () {
      var text = document.getElementById("st-restart-text");
      document.getElementById("st-restart-buttons").hidden = true;
      text.textContent = RESTART_WAIT;
      serviceAction("/api/node/restart", null, el.nodeStatus, function () {
        closeConfirm(false);
      });
    });
    return root;
  }

  function openConfirm() {
    service.confirming = true;
    el.restart.hidden = true;
    document.getElementById("st-restart-text").textContent = "The node stops for about a minute. Mining resumes by itself.";
    document.getElementById("st-restart-buttons").hidden = false;
    el.confirm.classList.add("st-open");
    el.restartNow.focus();
  }

  function closeConfirm(moveFocus) {
    service.confirming = false;
    document.getElementById("st-restart-text").textContent = "";
    document.getElementById("st-restart-buttons").hidden = true;
    el.confirm.classList.remove("st-open");
    el.restart.hidden = false;
    if (moveFocus) el.restart.focus();
  }

  function renderNode(s) {
    var d = el.details;
    d.status.textContent = s.running ? "Running" : "Stopped";
    if (!s.running) {
      ["sync", "up", "peers", "version", "chain"].forEach(function (k) { d[k].textContent = "–"; });
      return;
    }
    d.sync.textContent = syncText(s);
    d.up.textContent = uptimeText(s);
    d.peers.textContent = show(s.peers, f.num);
    d.version.textContent = versionText(s.clientVersion);
    d.chain.textContent = show(s.chainId);
  }

  // Service actions: restart and updates, one at a time.

  function setServiceDisabled(on) {
    var off = on || !service.available;
    el.restart.disabled = off;
    el.restartNow.disabled = on;
    el.restartCancel.disabled = on;
    el.updatesRun.disabled = off;
    el.updatesSwitch.disabled = off || !service.installed || service.on === null;
  }

  function serviceAction(url, waiting, box, done) {
    if (service.busy) return;
    service.busy = true;
    setServiceDisabled(true);
    if (waiting) showResult(box, { ok: true, message: waiting });
    if (box === el.updatesStatus) {
      el.updatesOutput.textContent = "";
      el.updatesOutput.hidden = true;
    }
    D.postJSON(url)
      .then(function (res) {
        showResult(box, res);
        if (box === el.updatesStatus) {
          el.updatesOutput.textContent = res.output || "";
          el.updatesOutput.hidden = !res.output;
        }
        if (res.updates) renderUpdates(res.updates);
      })
      .catch(function (err) {
        showResult(box, { ok: false, error: err.message });
      })
      .then(function () {
        service.busy = false;
        if (done) done();
        setServiceDisabled(false);
        D.refresh();
      });
  }

  D.screen("settings", {
    title: "Settings",
    sentence: "Every change applies at once and is kept after restarts and updates.",
    mount: function (root) {
      loadStyles();
      root.appendChild(h("div", { class: "st-grid" }, [miningCard(), payoutCard(), updatesCard(), nodeCard()]));
      root.appendChild(h("p", { class: "st-foot", text: "Only this computer can open this dashboard." }));

      D.poll("/api/mining", renderMining, function (err) {
        mining.running = false;
        if (!mining.busy) setMiningDisabled(true);
        setNote(el.miningNote, err.message);
      });
      D.poll("/api/node", renderNode, function (err) {
        el.details.status.textContent = "No answer";
        setNote(el.restartNote, err.message);
      });
      D.poll("/api/updates", renderUpdates, function (err) {
        setNote(el.updatesNote, err.message);
      });
    },
  });
})(GhostDash);
