"use strict";

// Overview: four number cards, the wall of the latest blocks, the mining card
// with the cores slider, the blocks won and what the node is doing. Pause and
// Resume mining sit at the top right. All of this screen's markup is built
// here and its look is in overview.css, loaded once when the screen mounts.
(function (D) {
  var h = D.h;
  var f = D.format;
  var show = f.show;

  var SVG = "http://www.w3.org/2000/svg";
  var STYLESHEET = "/static/screens/overview.css";
  var GHOST_PATH = "M37 50a13 13 0 0 1 26 0v13l-6.5-5.2-6.5 5.2-6.5-5.2-6.5 5.2z";
  var WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  // A win this recent (and with a known reward) gets the mint "+X now" tag.
  var NEW_WIN_MS = 10 * 60 * 1000;
  var LISTED = 4;

  function el(id) {
    return document.getElementById(id);
  }

  function text(id, value) {
    el(id).textContent = value;
  }

  function svg(tag, attrs, kids) {
    var node = document.createElementNS(SVG, tag);
    Object.keys(attrs || {}).forEach(function (key) {
      node.setAttribute(key, attrs[key]);
    });
    (kids || []).forEach(function (kid) {
      node.appendChild(kid);
    });
    return node;
  }

  function ghost(cls) {
    return svg("svg", { viewBox: "37 37 26 26", class: cls, "aria-hidden": "true", focusable: "false" }, [
      svg("path", { fill: "#FFFFFF", d: GHOST_PATH }),
    ]);
  }

  function loadStylesheet() {
    if (document.querySelector('link[href="' + STYLESHEET + '"]')) return;
    document.head.appendChild(h("link", { rel: "stylesheet", href: STYLESHEET }));
  }

  // Formatting for this screen.

  // coins cuts a decimal string of coins to at most places decimals, without
  // going through a float.
  function coins(s, places) {
    if (s === null || s === undefined || s === "") return null;
    var parts = String(s).split(".");
    var frac = (parts[1] || "").slice(0, places);
    while (frac.length < places) frac += "0";
    return places > 0 ? parts[0] + "." + frac : parts[0];
  }

  // estimate is "~" and the largest whole unit: ~3 d, ~4 h, ~35 min, ~20 s.
  function estimate(secs) {
    secs = Math.max(0, Math.round(secs));
    if (secs >= 86400) return "~" + Math.floor(secs / 86400) + " d";
    if (secs >= 3600) return "~" + Math.floor(secs / 3600) + " h";
    if (secs >= 60) return "~" + Math.floor(secs / 60) + " min";
    return "~" + secs + " s";
  }

  function pad2(n) {
    return n < 10 ? "0" + n : String(n);
  }

  function clock(d) {
    return pad2(d.getHours()) + ":" + pad2(d.getMinutes());
  }

  function startOfDay(d) {
    return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  }

  // when says when a block was won: "3 min ago", "4 h ago" (today),
  // "Yesterday 21:14", "Mon 09:30" (this week), else the date.
  function when(unixSecs) {
    var then = new Date(unixSecs * 1000);
    var now = new Date();
    var diff = now.getTime() - then.getTime();
    if (diff < 60 * 1000) return "Just now";
    if (diff < 3600 * 1000) return Math.floor(diff / 60000) + " min ago";
    var today = startOfDay(now);
    if (then.getTime() >= today) return Math.floor(diff / 3600000) + " h ago";
    var dayMs = 86400 * 1000;
    if (then.getTime() >= today - dayMs) return "Yesterday " + clock(then);
    if (then.getTime() >= today - 6 * dayMs) return WEEKDAYS[then.getDay()] + " " + clock(then);
    return then.toLocaleDateString();
  }

  // rateParts splits "1.84 MH/s" into the number and the unit.
  function rateParts(hps) {
    var s = f.rate(hps);
    var i = s.indexOf(" ");
    return { value: s.slice(0, i), unit: s.slice(i + 1) };
  }

  function sharePercent(share) {
    var p = share * 100;
    if (p === 0) return "0";
    if (p < 0.01) return "<0.01";
    return p.toFixed(p < 1 ? 2 : 1);
  }

  // logTime is the HH:MM of a line like "INFO [10-28|10:00:03.000] …".
  function logTime(line) {
    var m = /\[\d\d-\d\d\|(\d\d:\d\d)/.exec(line);
    return m ? m[1] : "–";
  }

  // What the screen has read so far; null until the first answer.
  var data = { node: null, mining: null, rewards: null, network: null, log: null };
  var errors = {};
  var screen = null;

  function stopped() {
    return (data.node !== null && !data.node.running) || (data.mining !== null && !data.mining.running);
  }

  // A node with no peers holds off mining until it connects to the network.
  var WAITING_FOR_PEERS = "Waiting for other nodes. Mining starts once your node connects to the network.";

  // waitingForPeers is true when mining is on but the node has 0 peers.
  function waitingForPeers() {
    var node = data.node;
    var mining = data.mining;
    return !stopped() && node !== null && node.peers === 0 && mining !== null && !mining.paused;
  }

  function sentence() {
    var node = data.node;
    var mining = data.mining;
    if (!node && !mining) return "Checking the node…";
    if (stopped()) {
      return "The node is stopped. Start it and this page will update by itself.";
    }
    if (waitingForPeers()) return WAITING_FOR_PEERS;
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

  // Top row: "Updated N s ago" and Pause / Resume mining.

  var stamp = h("span", { class: "mono", text: "–" });

  function markUpdated() {
    stamp.dataset.at = String(Date.now());
    stamp.textContent = f.ago(0);
  }

  function pauseIcon() {
    return svg("svg", { viewBox: "0 0 24 24", class: "ov-icon", "aria-hidden": "true", focusable: "false" }, [
      svg("path", { d: "M9 5v14M15 5v14", fill: "none", stroke: "#FFFFFF", "stroke-width": "2.6", "stroke-linecap": "round" }),
    ]);
  }

  function playIcon() {
    return svg("svg", { viewBox: "0 0 24 24", class: "ov-icon", "aria-hidden": "true", focusable: "false" }, [
      svg("path", { d: "M7 5l12 7-12 7z", fill: "#FFFFFF", stroke: "#FFFFFF", "stroke-width": "2", "stroke-linejoin": "round" }),
    ]);
  }

  function topRow() {
    // Pausing writes the mining-paused file in the node folder; until the
    // installer reads it, a node restart resumes mining.
    var pause = h("button", { type: "button", id: "mining-pause", class: "ov-pause", disabled: true }, [pauseIcon(), "Pause mining"]);
    var resume = h("button", { type: "button", id: "mining-resume", class: "ov-pause ov-resume", hidden: true, disabled: true }, [playIcon(), "Resume mining"]);
    pause.addEventListener("click", function () {
      control("/api/mining/pause", undefined, "ov-pause-result");
    });
    resume.addEventListener("click", function () {
      control("/api/mining/resume", undefined, "ov-pause-result");
    });
    return h("div", { class: "ov-top" }, [
      h("div", { class: "ov-top-row" }, [h("p", { class: "updated" }, ["Updated ", stamp]), pause, resume]),
      h("p", { class: "message", id: "ov-pause-result", role: "status", hidden: true }),
    ]);
  }

  // The note shown while the node waits for other nodes.

  function peersNote() {
    return h("section", { class: "ov-peers-note", id: "ov-peers-note", role: "status", hidden: true }, [
      h("p", { text: WAITING_FOR_PEERS }),
      h("a", { href: "#/network", class: "ov-link", text: "Network" }),
    ]);
  }

  function renderPeersNote() {
    el("ov-peers-note").hidden = !waitingForPeers();
  }

  // Number cards.

  function numberCard(id, label, unit, extra) {
    var labelRow = h("div", { class: "ov-label-row" }, [h("span", { class: "ov-label", id: id + "-label", text: label })]);
    if (extra) labelRow.appendChild(extra);
    var kids = [
      labelRow,
      h("p", { class: "ov-value" }, [
        h("span", { class: "mono", id: id + "-value", text: "–" }),
        h("span", { class: "ov-unit", id: id + "-unit", text: unit, hidden: true }),
      ]),
    ];
    if (id === "ov-hash") {
      kids.push(svg("svg", { viewBox: "0 0 160 22", preserveAspectRatio: "none", class: "ov-spark", "aria-hidden": "true", focusable: "false" }, [
        svg("polyline", { id: "ov-spark-line", fill: "none", stroke: "#6B4CFF", "stroke-width": "1.6", "stroke-linejoin": "round", "stroke-linecap": "round", points: "" }),
      ]));
    }
    kids.push(h("p", { class: "ov-hint", id: id + "-hint", text: "Checking the node…" }));
    return h("section", { class: "ov-num", "aria-labelledby": id + "-label" }, kids);
  }

  function numbers() {
    var mint = h("span", { class: "ov-mint", id: "ov-earned-new", hidden: true });
    return h("div", { class: "ov-numbers" }, [
      numberCard("ov-earned", "Earned", "GHOST", mint),
      numberCard("ov-next", "Next block (estimate)", ""),
      numberCard("ov-hash", "Hash rate", ""),
      numberCard("ov-share", "Your share", "%"),
    ]);
  }

  function setNumber(id, value, unit, hint) {
    text(id + "-value", value === null || value === undefined ? "–" : value);
    var u = el(id + "-unit");
    if (unit !== undefined) u.textContent = unit;
    u.hidden = value === null || value === undefined || !u.textContent;
    var hintEl = el(id + "-hint");
    hintEl.textContent = hint || "";
    hintEl.hidden = !hint;
  }

  // noMiningHint explains a missing mining number.
  function noMiningHint() {
    if (data.mining && data.mining.paused) return "Mining is paused";
    if (data.node && data.node.syncing === true) return "Waits until the node is in sync";
    return "No estimate yet";
  }

  function renderNumbers() {
    var m = data.mining;
    var r = data.rewards;
    var mint = el("ov-earned-new");
    var line = el("ov-spark-line");
    if (stopped()) {
      ["ov-earned", "ov-next", "ov-hash", "ov-share"].forEach(function (id) {
        setNumber(id, null, undefined, "Node not running");
      });
      mint.hidden = true;
      line.setAttribute("points", "");
      return;
    }

    if (r) {
      var won = r.wonToday === 1 ? "1 block" : show(r.wonToday, f.num) + " blocks";
      var today = coins(r.rewardToday && r.rewardToday.coins, 2);
      setNumber("ov-earned", coins(r.rewardTotal && r.rewardTotal.coins, 2), undefined,
        "+" + show(today) + " today · " + won);
      var newest = r.blocks && r.blocks[0];
      var fresh = newest && newest.reward && Date.now() - newest.time * 1000 < NEW_WIN_MS;
      mint.hidden = !fresh;
      mint.textContent = fresh ? "+" + coins(newest.reward.coins, 2) + " now" : "";
    } else {
      setNumber("ov-earned", null, undefined, errors.rewards || "Checking the node…");
      mint.hidden = true;
    }

    if (!m) {
      var waiting = errors.mining || "Checking the node…";
      setNumber("ov-next", null, undefined, waiting);
      setNumber("ov-hash", null, undefined, waiting);
      setNumber("ov-share", null, undefined, waiting);
      line.setAttribute("points", "");
      return;
    }

    if (m.expectedBlockSeconds === null || m.expectedBlockSeconds === undefined) {
      setNumber("ov-next", null, "", noMiningHint());
    } else {
      setNumber("ov-next", estimate(m.expectedBlockSeconds), "", "Could be much sooner or later");
    }

    if (m.hashRate === null || m.hashRate === undefined) {
      setNumber("ov-hash", null, "", noMiningHint());
    } else {
      var parts = rateParts(m.hashRate);
      setNumber("ov-hash", parts.value, parts.unit, "");
    }
    line.setAttribute("points", sparkPoints(m.hashRateTrend || []));

    if (m.networkShare === null || m.networkShare === undefined) {
      setNumber("ov-share", null, "%", noMiningHint());
    } else {
      var net = data.network && data.network.networkHashRate;
      setNumber("ov-share", sharePercent(m.networkShare), "%",
        net === null || net === undefined ? "Network hash rate unknown" : "of " + f.rate(net) + " on the network");
    }
  }

  // sparkPoints spreads the samples across x 0 to 160 and scales them into
  // y 3 to 19, a higher rate higher up. Fewer than 2 samples draw no line.
  function sparkPoints(trend) {
    if (trend.length < 2) return "";
    var rates = trend.map(function (s) { return s.hashRate; });
    var lo = Math.min.apply(null, rates);
    var hi = Math.max.apply(null, rates);
    return rates.map(function (v, i) {
      var x = (i / (rates.length - 1)) * 160;
      var y = hi === lo ? 11 : 19 - ((v - lo) / (hi - lo)) * 16;
      return x.toFixed(1) + "," + y.toFixed(1);
    }).join(" ");
  }

  // The block wall.

  function cardHead(id, title, link) {
    return h("div", { class: "ov-head" }, [h("h2", { id: id, text: title }), link]);
  }

  function swatch(cls, label) {
    return h("li", {}, [h("span", { class: "ov-swatch " + cls, "aria-hidden": "true" }), label]);
  }

  function wallCard() {
    return h("section", { class: "card ov-wall-card", "aria-labelledby": "ov-wall-title" }, [
      cardHead("ov-wall-title", "Last 240 blocks", h("a", { href: "#/network", class: "ov-link", text: "Network" })),
      h("div", { id: "ov-wall", class: "ov-wall", role: "img", "aria-label": "No blocks yet.", hidden: true }),
      h("p", { id: "ov-wall-empty", class: "ov-empty", text: "Checking the node…" }),
      h("ul", { class: "ov-legend" }, [
        swatch("ov-swatch-newest", "Newest"),
        swatch("ov-swatch-other", "Other nodes"),
        swatch("ov-swatch-own", "Won by you"),
        swatch("ov-swatch-lost", "Found by you, lost the race"),
      ]),
    ]);
  }

  function cellTitle(b, newest) {
    var who;
    if (b.own) who = "won by you";
    else if (b.lostRace) who = "found by you, another node published it first";
    else who = "mined by " + (b.minerShort || "another node");
    return "Block " + f.num(b.number) + (newest ? ", the newest" : "") + ", " + who;
  }

  var wallKey = null;

  function renderWall() {
    var wall = el("ov-wall");
    var empty = el("ov-wall-empty");
    var blocks = (data.network && data.network.wall) || [];
    var message = null;
    if (stopped() || (data.network && !data.network.running)) message = "No blocks to show while the node is stopped.";
    else if (!data.network) message = errors.network || "Checking the node…";
    else if (!blocks.length) message = "No blocks yet.";

    var key = message || JSON.stringify(blocks);
    if (key === wallKey) return;
    wallKey = key;

    empty.hidden = !message;
    empty.textContent = message || "";
    wall.hidden = !!message;
    D.clear(wall);
    if (message) return;

    var won = 0;
    var lost = 0;
    blocks.forEach(function (b, i) {
      var cls = "ov-cell";
      if (b.own) {
        cls += " ov-cell-own";
        won++;
      } else if (b.lostRace) {
        cls += " ov-cell-lost";
        lost++;
      } else if (i === 0) {
        cls += " ov-cell-newest";
      }
      var cell = h("div", { class: cls, title: cellTitle(b, i === 0) });
      if (b.own) cell.appendChild(ghost("ov-ghost"));
      wall.appendChild(cell);
    });
    wall.setAttribute("aria-label", "The last " + blocks.length + " blocks, newest first. Your node won " + won +
      " and found " + lost + " more that another node published first.");
  }

  // The mining card. The slider follows the saved core count until the
  // person moves it.

  var coresTouched = false;
  var busy = false;

  function miningCard() {
    var input = h("input", { type: "range", id: "cores-input", min: 1, max: 1, step: 1, value: 1, disabled: true });
    var less = h("button", { type: "button", id: "ov-cores-less", class: "ov-step", "aria-label": "One core fewer", disabled: true, text: "−" });
    var more = h("button", { type: "button", id: "ov-cores-more", class: "ov-step", "aria-label": "One core more", disabled: true, text: "+" });

    input.addEventListener("input", function () {
      coresTouched = true;
      text("ov-cores-value", input.value + " of " + input.max);
    });
    input.addEventListener("change", function () {
      applyCores(parseInt(input.value, 10));
    });
    less.addEventListener("click", function () {
      applyCores(parseInt(input.value, 10) - 1);
    });
    more.addEventListener("click", function () {
      applyCores(parseInt(input.value, 10) + 1);
    });

    return h("section", { class: "card ov-mining", "aria-labelledby": "ov-mining-title" }, [
      h("h2", { id: "ov-mining-title", text: "Mining" }),
      h("label", { class: "ov-cores-label", for: "cores-input" }, [
        h("span", { text: "Cores" }),
        h("span", { class: "mono", id: "ov-cores-value", text: "–" }),
      ]),
      h("div", { class: "ov-cores-row" }, [less, input, more]),
      h("div", { class: "ov-scale", "aria-hidden": "true" }, [h("span", { text: "Quieter" }), h("span", { text: "Faster" })]),
      h("p", { class: "message", id: "ov-cores-result", role: "status", hidden: true }),
      h("p", { class: "ov-payout" }, [
        "Paid to ",
        h("span", { class: "mono ov-address", id: "ov-payout", text: "–" }),
        h("a", { href: "#/settings", class: "ov-change", text: "Change" }),
      ]),
      h("p", { class: "ov-note", text: "Changes apply at once and are kept after restarts and updates." }),
    ]);
  }

  function applyCores(n) {
    var input = el("cores-input");
    var max = parseInt(input.max, 10);
    n = Math.max(1, Math.min(max, n));
    input.value = String(n);
    text("ov-cores-value", n + " of " + max);
    control("/api/mining/cores", { cores: n }, "ov-cores-result");
  }

  function renderMining() {
    var m = data.mining;
    var input = el("cores-input");
    if (m && !stopped()) {
      input.max = String(m.logicalCores);
      if (!coresTouched) {
        input.value = String(m.coresUsed);
        text("ov-cores-value", m.coresUsed + " of " + m.logicalCores);
      }
    } else if (!coresTouched) {
      text("ov-cores-value", "–");
    }
    var payout = (m && m.payoutAddress) || (data.rewards && data.rewards.payoutAddress) || null;
    if (!m && !data.rewards) text("ov-payout", "–");
    else text("ov-payout", payout ? f.shortAddress(payout) : "not set");
    el("ov-payout").title = payout || "";

    var paused = !!(m && m.paused);
    el("mining-pause").hidden = paused;
    el("mining-resume").hidden = !paused;
    updateControls();
  }

  // updateControls disables every control while a request is in flight,
  // before the first answer and while the node is stopped.
  function updateControls() {
    var off = busy || !data.mining || stopped();
    ["cores-input", "ov-cores-less", "ov-cores-more", "mining-pause", "mining-resume"].forEach(function (id) {
      el(id).disabled = off;
    });
    if (!off) {
      var input = el("cores-input");
      el("ov-cores-less").disabled = parseInt(input.value, 10) <= 1;
      el("ov-cores-more").disabled = parseInt(input.value, 10) >= parseInt(input.max, 10);
    }
  }

  function showResult(id, msg, failed) {
    var box = el(id);
    box.textContent = msg || "";
    box.hidden = !msg;
    box.className = failed ? "message error" : "message";
  }

  function control(url, body, resultId) {
    if (busy) return;
    busy = true;
    updateControls();
    showResult(resultId, "Asking the node…", false);
    D.postJSON(url, body)
      .then(function (res) {
        showResult(resultId, res.ok ? res.message : res.error, !res.ok);
        coresTouched = false;
        D.refresh();
      })
      .catch(function (err) {
        showResult(resultId, err.message, true);
      })
      .then(function () {
        busy = false;
        updateControls();
      });
  }

  // Blocks you won and what the node is doing.

  function listCard(id, title, link) {
    return h("section", { class: "card ov-list-card", "aria-labelledby": id + "-title" }, [
      cardHead(id + "-title", title, link),
      h("p", { id: id + "-empty", class: "ov-empty", text: "Checking the node…" }),
      h(id === "ov-log" ? "ol" : "ul", { id: id + "-list", class: "ov-list", hidden: true }),
    ]);
  }

  function setList(id, rows, emptyText) {
    var list = el(id + "-list");
    var empty = el(id + "-empty");
    D.clear(list);
    rows.forEach(function (row) { list.appendChild(row); });
    list.hidden = !rows.length;
    empty.hidden = rows.length > 0;
    empty.textContent = rows.length ? "" : emptyText;
  }

  function renderWon() {
    var r = data.rewards;
    var all = el("ov-won-all");
    all.textContent = "All " + (r && typeof r.wonTotal === "number" ? f.num(r.wonTotal) : "–");
    if (stopped()) return setList("ov-won", [], "Nothing yet.");
    if (!r) return setList("ov-won", [], errors.rewards || "Checking the node…");
    var rows = (r.blocks || []).slice(0, LISTED).map(function (b) {
      return h("li", { class: "ov-row" }, [
        h("span", { class: "ov-tile", "aria-hidden": "true" }, [ghost("ov-tile-ghost")]),
        h("span", { class: "mono ov-won-number", text: f.num(b.number) }),
        h("span", { class: "ov-won-when", text: when(b.time) }),
        h("span", { class: "mono ov-won-reward", text: b.reward ? "+" + coins(b.reward.coins, 5) : "–" }),
      ]);
    });
    setList("ov-won", rows, "Nothing yet.");
  }

  function renderLog() {
    var l = data.log;
    if (stopped()) return setList("ov-log", [], "Nothing yet.");
    if (!l) return setList("ov-log", [], errors.log || "Checking the node…");
    if (!l.available) return setList("ov-log", [], l.message || "The node log is not available.");
    var rows = (l.lines || []).filter(function (line) { return line.meaning; }).slice(0, LISTED).map(function (line) {
      return h("li", { class: "ov-row" }, [
        h("span", { class: "mono ov-log-time", text: logTime(line.text) }),
        h("span", { class: "ov-log-meaning", text: line.meaning }),
      ]);
    });
    setList("ov-log", rows, "Nothing yet.");
  }

  function renderAll() {
    screen.setSentence(sentence());
    renderPeersNote();
    renderNumbers();
    renderWall();
    renderMining();
    renderWon();
    renderLog();
  }

  function watch(key, url) {
    D.poll(url, function (d) {
      data[key] = d;
      errors[key] = null;
      markUpdated();
      renderAll();
    }, function (err) {
      errors[key] = err.message;
      renderAll();
    });
  }

  D.screen("overview", {
    title: "Overview",
    sentence: "Checking the node…",
    mount: function (root, s) {
      screen = s;
      loadStylesheet();
      root.appendChild(peersNote());
      root.appendChild(topRow());
      root.appendChild(numbers());
      root.appendChild(h("div", { class: "ov-pair" }, [wallCard(), miningCard()]));
      root.appendChild(h("div", { class: "ov-pair" }, [
        listCard("ov-won", "Blocks you won", h("a", { href: "#/rewards", class: "ov-link", id: "ov-won-all", text: "All –" })),
        listCard("ov-log", "What the node is doing", h("a", { href: "#/network", class: "ov-link", text: "Full log" })),
      ]));
      watch("node", "/api/node");
      watch("mining", "/api/mining");
      watch("rewards", "/api/rewards");
      watch("network", "/api/network");
      watch("log", "/api/log");
    },
  });
})(GhostDash);
