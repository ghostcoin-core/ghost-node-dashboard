"use strict";

(function () {
  var REFRESH_MS = 5000;
  var NOT_RUNNING = "Node not running. Start it and this panel will update by itself.";

  function el(id) {
    return document.getElementById(id);
  }

  function text(id, value) {
    el(id).textContent = value;
  }

  function show(v, format) {
    if (v === null || v === undefined) return "–";
    return format ? format(v) : String(v);
  }

  function num(n) {
    return n.toLocaleString();
  }

  function duration(secs) {
    secs = Math.max(0, Math.round(secs));
    var d = Math.floor(secs / 86400);
    var h = Math.floor((secs % 86400) / 3600);
    var m = Math.floor((secs % 3600) / 60);
    if (d > 0) return d + " d " + h + " h";
    if (h > 0) return h + " h " + m + " min";
    if (m > 0) return m + " min " + (secs % 60) + " s";
    return secs + " s";
  }

  function rate(hps) {
    var units = ["H/s", "kH/s", "MH/s", "GH/s", "TH/s", "PH/s"];
    var i = 0;
    while (hps >= 1000 && i < units.length - 1) {
      hps /= 1000;
      i++;
    }
    return (i === 0 ? Math.round(hps) : hps.toFixed(2)) + " " + units[i];
  }

  function percent(share) {
    var p = share * 100;
    if (p === 0) return "0 %";
    if (p < 0.01) return "under 0.01 %";
    return p.toFixed(p < 1 ? 2 : 1) + " %";
  }

  function coins(a) {
    return a.coins + " GHOST";
  }

  function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  // A panel is one card fed by one API endpoint. A local panel reads this
  // computer rather than the node and sets its own state.
  function Panel(id, url, render, local) {
    this.root = el(id);
    this.url = url;
    this.render = render;
    this.local = !!local;
  }

  Panel.prototype.part = function (name) {
    return this.root.querySelector("[data-" + name + "]");
  };

  Panel.prototype.setState = function (label, kind) {
    var s = this.part("state");
    s.textContent = label;
    s.className = "pill pill-" + kind;
  };

  Panel.prototype.setMessage = function (msg) {
    var m = this.part("message");
    m.textContent = msg || "";
    m.hidden = !msg;
  };

  Panel.prototype.refresh = function () {
    var panel = this;
    fetch(panel.url, { credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (r.status === 401) {
          throw new Error("This browser is not signed in. Open the dashboard from the link the installer printed.");
        }
        if (!r.ok) throw new Error("The dashboard answered " + r.status + ".");
        return r.json();
      })
      .then(function (data) {
        if (panel.local || data.running) {
          panel.setMessage("");
        } else {
          panel.setState("Node not running", "bad");
          panel.setMessage(data.error === "node not running" ? NOT_RUNNING : data.error || NOT_RUNNING);
        }
        panel.render(panel, data);
        panel.part("updated").textContent = new Date().toLocaleTimeString();
      })
      .catch(function (err) {
        panel.setState("No answer", "bad");
        panel.setMessage(err.message);
      });
  };

  function renderNode(panel, s) {
    var progress = el("node-progress");
    progress.hidden = true;
    if (s.running) {
      if (s.syncing === true) {
        panel.setState("Running, syncing", "wait");
      } else {
        panel.setState("Running", "ok");
      }
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
      text("node-blocks", num(s.currentBlock) + " of " + show(s.highestBlock, num));
    } else {
      text("node-blocks", show(s.currentBlock, num));
    }
    text("node-peers", show(s.peers));
    text("node-clients", show(s.peerClients, function (list) {
      if (!list.length) return "none";
      return list.map(function (c) { return c.count > 1 ? c.name + " ×" + c.count : c.name; }).join(", ");
    }));
    text("node-uptime", show(s.uptimeSeconds, function (secs) {
      return s.uptimeFrom === "first answer" ? "at least " + duration(secs) : duration(secs);
    }));
    text("node-network", show(s.networkName));
    text("node-autoupdate", show(s.autoUpdate, function (on) { return on ? "On" : "Off"; }));
    text("node-version", show(s.clientVersion));
  }

  function renderMining(panel, s) {
    if (s.running) {
      if (s.mining === true) panel.setState("Mining", "ok");
      else if (s.mining === false) panel.setState("Paused", "wait");
      else panel.setState("Running", "wait");
    }
    text("mining-cores", s.coresUsed + " of " + s.logicalCores);
    el("mining-hashrate-row").hidden = !s.hashRate;
    text("mining-hashrate", show(s.hashRate, rate));
    text("mining-share", show(s.networkShare, percent));
    text("mining-next", show(s.expectedBlockSeconds, function (secs) { return "about " + duration(secs); }));
    text("mining-payout", show(s.payoutAddress));
    var copy = el("mining-copy");
    copy.hidden = !s.payoutAddress;
    copy.dataset.value = s.payoutAddress || "";
    renderControls(s);
    text("mining-note", "Estimates assume a target block time of " + s.targetBlockSeconds +
      " s and change with the network.");
  }

  // The slider follows the saved core count until the person moves it.
  var coresTouched = false;
  var controlBusy = false;

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

  function setControlsBusy(busy) {
    controlBusy = busy;
    ["cores-apply", "mining-pause", "mining-resume"].forEach(function (id) {
      el(id).disabled = busy;
    });
  }

  function showResult(msg, failed) {
    var box = el("control-result");
    box.textContent = msg;
    box.hidden = !msg;
    box.className = failed ? "message error" : "message";
  }

  function control(url, body) {
    if (controlBusy) return;
    setControlsBusy(true);
    showResult("Asking the node…", false);
    fetch(url, {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    })
      .then(function (r) {
        if (r.status === 401) {
          throw new Error("This browser is not signed in. Open the dashboard from the link the installer printed.");
        }
        return r.json().catch(function () {
          throw new Error("The dashboard answered " + r.status + ".");
        });
      })
      .then(function (res) {
        showResult(res.ok ? res.message : res.error, !res.ok);
        coresTouched = false;
        panels[1].refresh();
      })
      .catch(function (err) {
        showResult(err.message, true);
      })
      .then(function () {
        setControlsBusy(false);
      });
  }

  el("cores-input").addEventListener("input", function () {
    coresTouched = true;
    text("cores-value", this.value + " of " + this.max);
  });

  el("cores-form").addEventListener("submit", function (e) {
    e.preventDefault();
    control("/api/mining/cores", { cores: parseInt(el("cores-input").value, 10) });
  });

  el("mining-pause").addEventListener("click", function () {
    control("/api/mining/pause");
  });

  el("mining-resume").addEventListener("click", function () {
    control("/api/mining/resume");
  });

  function renderNetwork(panel, s) {
    if (s.running) panel.setState("Connected", "ok");
    text("network-height", show(s.height, num));
    text("network-blocktime", show(s.averageBlockSeconds, function (secs) {
      return secs.toFixed(1) + " s (last " + s.averageOverBlocks + " blocks)";
    }));
    text("network-difficulty", show(s.difficulty, num));
    text("network-hashrate", show(s.networkHashRate, rate));
    text("network-note", "The network hash rate is an estimate: difficulty ÷ the " +
      s.targetBlockSeconds + " s target block time. Your own blocks are in violet.");

    var body = el("network-blocks");
    while (body.firstChild) body.removeChild(body.firstChild);
    (s.blocks || []).forEach(function (b) {
      var row = document.createElement("tr");
      if (b.own) row.className = "own";
      [num(b.number), new Date(b.time * 1000).toLocaleTimeString(), b.minerShort + (b.own ? " (you)" : "")]
        .forEach(function (v, i) {
          var cell = document.createElement("td");
          cell.textContent = v;
          if (i !== 1) cell.className = "mono";
          if (i === 2) cell.title = b.miner;
          row.appendChild(cell);
        });
      body.appendChild(row);
    });
  }

  function renderRewards(panel, s) {
    if (s.running) {
      panel.setState(s.indexing ? "Reading blocks" : "Up to date", s.indexing ? "wait" : "ok");
    }
    text("rewards-today-count", show(s.wonToday, num));
    text("rewards-today", coins(s.rewardToday));
    text("rewards-total-count", show(s.wonTotal, num));
    text("rewards-total", coins(s.rewardTotal));
    text("rewards-balance", show(s.balance, coins));
    text("rewards-lost", show(s.lostRaces, num));
    text("rewards-lost-note", "A lost race: " + s.lostRacesNote);

    var body = el("rewards-blocks");
    clear(body);
    (s.blocks || []).forEach(function (b) {
      var row = document.createElement("tr");
      [num(b.number), new Date(b.time * 1000).toLocaleString(), b.reward ? coins(b.reward) : "unknown"]
        .forEach(function (v, i) {
          var cell = document.createElement("td");
          cell.textContent = v;
          if (i !== 1) cell.className = "mono";
          if (i === 0) cell.title = b.hash;
          row.appendChild(cell);
        });
      body.appendChild(row);
    });

    var notes = [];
    if (!s.payoutAddress) notes.push("The node has no payout address, so it cannot win blocks.");
    else if (!s.blocks || !s.blocks.length) notes.push("No blocks won yet.");
    if (s.wonTotal > (s.blocks || []).length) notes.push("Showing the latest " + s.blocks.length + " blocks won.");
    if (s.indexing) {
      notes.push("Reading the chain for your blocks: " +
        (s.indexing.readTo === null ? "starting" : "block " + num(s.indexing.readTo)) +
        " of " + num(s.indexing.height) + ".");
    }
    if (s.unknownRewards) {
      notes.push(s.unknownRewards + " block(s) won show no reward: the node no longer keeps the balances from then, so they are left out of the sums.");
    }
    notes.push("Each reward is your balance change across the block. Today is in this computer's time.");
    if (s.indexError) notes.push("Index problem: " + s.indexError);
    text("rewards-note", notes.join(" "));
  }

  function renderLog(panel, s) {
    if (s.available) panel.setState("Read only", "ok");
    else panel.setState("No log", "wait");
    text("log-note", s.available
      ? "The last " + s.lines.length + " lines of " + s.path + ", newest first."
      : s.message + " (" + s.path + ")");
    var list = el("log-lines");
    clear(list);
    s.lines.forEach(function (line) {
      var item = document.createElement("li");
      var code = document.createElement("code");
      code.textContent = line.text;
      item.appendChild(code);
      if (line.meaning) {
        var meaning = document.createElement("span");
        meaning.className = "meaning";
        meaning.textContent = line.meaning;
        item.appendChild(meaning);
      }
      list.appendChild(item);
    });
  }

  el("mining-copy").addEventListener("click", function () {
    var button = this;
    var value = button.dataset.value;
    if (!value || !navigator.clipboard) return;
    navigator.clipboard.writeText(value).then(function () {
      button.textContent = "Copied";
      setTimeout(function () { button.textContent = "Copy"; }, 1500);
    });
  });

  var panels = [
    new Panel("node-panel", "/api/node", renderNode),
    new Panel("mining-panel", "/api/mining", renderMining),
    new Panel("network-panel", "/api/network", renderNetwork),
    new Panel("rewards-panel", "/api/rewards", renderRewards),
    new Panel("log-panel", "/api/log", renderLog, true),
  ];

  function refresh() {
    panels.forEach(function (p) { p.refresh(); });
  }

  refresh();
  setInterval(refresh, REFRESH_MS);
})();
