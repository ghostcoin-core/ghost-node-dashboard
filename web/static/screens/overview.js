"use strict";

// Overview: the node panel and the mining panel with the cores slider and
// Pause/Resume mining. All of this screen's markup is built here.
(function (D) {
  var h = D.h;
  var f = D.format;
  var show = f.show;

  function el(id) {
    return document.getElementById(id);
  }

  function text(id, value) {
    el(id).textContent = value;
  }

  // The sentence under the title reads both panels.
  var node = null;
  var mining = null;
  var screen = null;

  function sentence() {
    if (!node && !mining) return "Checking the node…";
    if ((node && !node.running) || (mining && !mining.running)) {
      return "The node is stopped. Start it and this page will update by itself.";
    }
    var parts = [];
    if (mining) {
      if (mining.paused) parts.push("Mining is paused.");
      else parts.push("Mining on " + mining.coresUsed + " of " + mining.logicalCores + " cores.");
    }
    if (node && node.syncing === true) {
      parts.push("Syncing, at block " + show(node.currentBlock, f.num) + " of " + show(node.highestBlock, f.num) + ".");
    } else if (node && node.currentBlock !== null && node.currentBlock !== undefined) {
      parts.push("In sync at block " + f.num(node.currentBlock) + ".");
    }
    return parts.join(" ");
  }

  function updateSentence() {
    screen.setSentence(sentence());
  }

  function nodeCard() {
    var c = D.card({ id: "node-panel", title: "Your node" });
    c.field("Sync", "node-sync");
    c.field("Block", "node-blocks", { mono: true });
    c.field("Peers", "node-peers", { mono: true });
    c.field("Up for", "node-uptime");
    c.field("Network", "node-network");
    c.field("Automatic updates", "node-autoupdate");
    c.field("Peer clients", "node-clients", { mono: true, wide: true });
    c.field("Client version", "node-version", { mono: true, wide: true });
    c.body.appendChild(h("progress", { id: "node-progress", max: 100, value: 0, hidden: true }));
    return c;
  }

  function renderNode(panel, s) {
    node = s;
    var progress = el("node-progress");
    progress.hidden = true;
    if (s.running) {
      if (s.syncing === true) panel.setState("Running, syncing", "wait");
      else panel.setState("Running", "ok");
    }
    if (s.syncing === true) {
      var pct = s.highestBlock ? Math.min(100, (s.currentBlock / s.highestBlock) * 100) : 0;
      text("node-sync", "Syncing, " + pct.toFixed(1) + " %");
      progress.hidden = false;
      progress.value = pct;
    } else {
      text("node-sync", show(s.syncing, function () { return "In sync"; }));
    }
    if (s.currentBlock !== null && s.currentBlock !== undefined && s.syncing === true) {
      text("node-blocks", f.num(s.currentBlock) + " of " + show(s.highestBlock, f.num));
    } else {
      text("node-blocks", show(s.currentBlock, f.num));
    }
    text("node-peers", show(s.peers));
    text("node-clients", show(s.peerClients, function (list) {
      if (!list.length) return "none";
      return list.map(function (c) { return c.count > 1 ? c.name + " ×" + c.count : c.name; }).join(", ");
    }));
    text("node-uptime", show(s.uptimeSeconds, function (secs) {
      return s.uptimeFrom === "first answer" ? "at least " + f.duration(secs) : f.duration(secs);
    }));
    text("node-network", show(s.networkName));
    text("node-autoupdate", show(s.autoUpdate, function (on) { return on ? "On" : "Off"; }));
    text("node-version", show(s.clientVersion));
    updateSentence();
  }

  // The slider follows the saved core count until the person moves it.
  var coresTouched = false;
  var busy = false;

  function miningCard() {
    var c = D.card({ id: "mining-panel", title: "Mining" });
    c.field("Cores used", "mining-cores", { mono: true });
    c.field("Hash rate", "mining-hashrate", { mono: true, rowId: "mining-hashrate-row" });
    c.field("Share of the network (estimate)", "mining-share", { mono: true });
    c.field("Next block in (estimate)", "mining-next");
    var payout = h("dd", { class: "copy-row" }, [
      h("span", { id: "mining-payout", class: "mono", text: "–" }),
      D.copyButton("mining-copy"),
    ]);
    c.fields.appendChild(h("div", { class: "wide" }, [h("dt", { text: "Payout address" }), payout]));
    c.body.appendChild(h("p", { class: "note", id: "mining-note" }));

    c.body.appendChild(h("h3", { text: "Controls" }));
    var input = h("input", { type: "range", id: "cores-input", min: 1, max: 1, step: 1, value: 1 });
    var form = h("form", { class: "controls", id: "cores-form" }, [
      h("label", { for: "cores-input" }, ["Cores to mine on: ", h("span", { id: "cores-value", class: "mono", text: "–" })]),
      h("div", { class: "control-row" }, [
        input,
        h("button", { type: "submit", id: "cores-apply", class: "action", text: "Apply" }),
      ]),
    ]);
    c.body.appendChild(form);
    var pause = h("button", { type: "button", id: "mining-pause", class: "action", hidden: true, text: "Pause mining" });
    var resume = h("button", { type: "button", id: "mining-resume", class: "action", hidden: true, text: "Resume mining" });
    c.body.appendChild(h("div", { class: "control-row" }, [pause, resume]));
    c.body.appendChild(h("p", { class: "message", id: "control-result", role: "status", hidden: true }));
    c.body.appendChild(h("p", { class: "note" }, [
      "The core count is saved in the node folder and applies at the next node restart: the node cannot change it while running. " +
      "Restarts and updates keep it. While mining is paused the node keeps syncing. " +
      "For now a node restart resumes mining, until the node installer learns to read the ",
      h("code", { text: "mining-paused" }),
      " file.",
    ]));

    input.addEventListener("input", function () {
      coresTouched = true;
      text("cores-value", this.value + " of " + this.max);
    });
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      control("/api/mining/cores", { cores: parseInt(input.value, 10) });
    });
    pause.addEventListener("click", function () {
      control("/api/mining/pause");
    });
    resume.addEventListener("click", function () {
      control("/api/mining/resume");
    });
    return c;
  }

  function renderMining(panel, s) {
    mining = s;
    if (s.running) {
      if (s.mining === true) panel.setState("Mining", "ok");
      else if (s.mining === false) panel.setState("Paused", "wait");
      else panel.setState("Running", "wait");
    }
    text("mining-cores", s.coresUsed + " of " + s.logicalCores);
    el("mining-hashrate-row").hidden = !s.hashRate;
    text("mining-hashrate", show(s.hashRate, f.rate));
    text("mining-share", show(s.networkShare, f.percent));
    text("mining-next", show(s.expectedBlockSeconds, function (secs) { return "about " + f.duration(secs); }));
    text("mining-payout", show(s.payoutAddress));
    var copy = el("mining-copy");
    copy.hidden = !s.payoutAddress;
    copy.dataset.value = s.payoutAddress || "";
    renderControls(s);
    text("mining-note", "Estimates assume a target block time of " + s.targetBlockSeconds +
      " s and change with the network.");
    updateSentence();
  }

  function renderControls(s) {
    var input = el("cores-input");
    input.max = String(s.logicalCores);
    if (!coresTouched) {
      input.value = String(s.coresUsed);
      text("cores-value", s.coresUsed + " of " + s.logicalCores);
    }
    el("mining-pause").hidden = s.paused;
    el("mining-resume").hidden = !s.paused;
  }

  function setBusy(on) {
    busy = on;
    ["cores-apply", "mining-pause", "mining-resume"].forEach(function (id) {
      el(id).disabled = on;
    });
  }

  function showResult(msg, failed) {
    var box = el("control-result");
    box.textContent = msg;
    box.hidden = !msg;
    box.className = failed ? "message error" : "message";
  }

  function control(url, body) {
    if (busy) return;
    setBusy(true);
    showResult("Asking the node…", false);
    D.postJSON(url, body)
      .then(function (res) {
        showResult(res.ok ? res.message : res.error, !res.ok);
        coresTouched = false;
        D.refresh();
      })
      .catch(function (err) {
        showResult(err.message, true);
      })
      .then(function () {
        setBusy(false);
      });
  }

  D.screen("overview", {
    title: "Overview",
    sentence: "Checking the node…",
    mount: function (root, s) {
      screen = s;
      var n = nodeCard();
      var m = miningCard();
      root.appendChild(n.root);
      root.appendChild(m.root);
      D.panel(n, "/api/node", renderNode);
      D.panel(m, "/api/mining", renderMining);
    },
  });
})(GhostDash);
