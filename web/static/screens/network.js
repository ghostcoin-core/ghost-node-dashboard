"use strict";

// Network: the four number cards, the latest blocks and the node log.
// All of this screen's markup is built here; its looks are in network.css,
// which the screen adds to the page once, when it mounts.
(function (D) {
  var h = D.h;
  var f = D.format;
  var show = f.show;

  var STYLESHEET = "/static/screens/network.css";
  var SVG_NS = "http://www.w3.org/2000/svg";
  var NOT_RUNNING = "Node not running";

  var screen = null;
  var view = {
    network: null, // the last /api/network answer
    rewards: {}, // block number -> reward coins, for your own blocks
    log: null, // the last /api/log answer
    yoursOnly: false,
    raw: false,
  };
  var el = {};

  function loadStyles() {
    var links = document.head.querySelectorAll('link[rel="stylesheet"]');
    for (var i = 0; i < links.length; i++) {
      if (links[i].getAttribute("href") === STYLESHEET) return;
    }
    document.head.appendChild(h("link", { rel: "stylesheet", href: STYLESHEET }));
  }

  // Formatting.

  // compact writes a large whole number short: 27.7 G, 412 M, 9.8 k, 950.
  function compact(n) {
    var units = ["", " k", " M", " G", " T", " P", " E"];
    var i = 0;
    while (n >= 1000 && i < units.length - 1) {
      n /= 1000;
      i++;
    }
    if (i === 0) return String(Math.round(n));
    var s = n >= 100 ? String(Math.round(n)) : n.toFixed(1);
    if (Number(s) >= 1000 && i < units.length - 1) {
      i++;
      s = (n / 1000).toFixed(1);
    }
    return s + units[i];
  }

  function seconds(secs) {
    return secs.toFixed(1) + " s";
  }

  // splitLog finds the HH:MM:SS of a line's bracketed stamp and the text
  // after the stamp. A line without a stamp has no time and is all text.
  function splitLog(text) {
    var m = /^[^\[]*\[([^\]]*)\]\s*(.*)$/.exec(text);
    var t = m && /(\d{2}:\d{2}:\d{2})/.exec(m[1]);
    if (!t) return { time: null, raw: text };
    return { time: t[1], raw: m[2] };
  }

  // Building blocks.

  function ghost() {
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("viewBox", "37 37 26 26");
    svg.setAttribute("width", "14");
    svg.setAttribute("height", "14");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    svg.setAttribute("class", "nw-ghost");
    var path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("fill", "#6B4CFF");
    path.setAttribute("d", "M37 50a13 13 0 0 1 26 0v13l-6.5-5.2-6.5 5.2-6.5-5.2-6.5 5.2z");
    svg.appendChild(path);
    return svg;
  }

  // segmented makes a pair of real buttons of which one is pressed.
  // onPick(index) is called when a button is chosen.
  function segmented(label, names, onPick) {
    var buttons = names.map(function (name, i) {
      var b = h("button", {
        type: "button",
        class: "nw-seg-button",
        "aria-pressed": i === 0 ? "true" : "false",
        text: name,
      });
      b.addEventListener("click", function () {
        buttons.forEach(function (other, j) {
          other.setAttribute("aria-pressed", j === i ? "true" : "false");
        });
        onPick(i);
      });
      return b;
    });
    return h("div", { class: "nw-seg", role: "group", "aria-label": label }, buttons);
  }

  function updatedLine() {
    var stamp = h("span", { class: "mono", text: "never" });
    return { root: h("p", { class: "updated" }, ["Updated ", stamp]), stamp: stamp };
  }

  function markUpdated(stamp) {
    stamp.dataset.at = String(Date.now());
    stamp.textContent = f.ago(0);
  }

  function setMessage(node, msg) {
    node.textContent = msg || "";
    node.hidden = !msg;
  }

  // The four number cards.

  var STATS = [
    { key: "height", label: "Height" },
    { key: "blocktime", label: "Block time" },
    { key: "hashrate", label: "Network hash rate" },
    { key: "difficulty", label: "Difficulty" },
  ];

  function statsRow() {
    el.stats = {};
    return h("div", { class: "nw-stats" }, STATS.map(function (s) {
      var value = h("p", { class: "nw-stat-value mono", id: "nw-" + s.key, text: "–" });
      var hint = h("p", { class: "nw-stat-hint", id: "nw-" + s.key + "-hint", text: "" });
      el.stats[s.key] = { value: value, hint: hint };
      return h("section", { class: "nw-stat", "aria-labelledby": "nw-" + s.key + "-label" }, [
        h("h2", { class: "nw-stat-label", id: "nw-" + s.key + "-label", text: s.label }),
        value,
        hint,
      ]);
    }));
  }

  function setStat(key, value, hint) {
    el.stats[key].value.textContent = value;
    el.stats[key].hint.textContent = hint;
  }

  function renderStats(s) {
    if (!s.running) {
      STATS.forEach(function (st) { setStat(st.key, "–", NOT_RUNNING); });
      return;
    }
    setStat("height", show(s.height, f.num), "newest block");
    setStat("blocktime", show(s.averageBlockSeconds, seconds),
      "average of the last " + show(s.averageOverBlocks, f.num));
    setStat("hashrate", show(s.networkHashRate, f.rate), "estimate");
    setStat("difficulty", show(s.difficulty, compact), "adjusts every block");
  }

  // Latest blocks.

  function blocksCard() {
    el.blocksMessage = h("p", { class: "message", id: "nw-blocks-message", hidden: true });
    el.blocks = h("tbody", { id: "nw-blocks" });
    el.blocksUpdated = updatedLine();
    var head = function (text, right) {
      return h("th", { scope: "col", class: right ? "nw-right" : "", text: text });
    };
    return h("section", { class: "card nw-card", id: "nw-blocks-card", "aria-labelledby": "nw-blocks-title" }, [
      h("div", { class: "nw-head" }, [
        h("h2", { id: "nw-blocks-title", text: "Latest blocks" }),
        segmented("Which blocks to show", ["All", "Yours only"], function (i) {
          view.yoursOnly = i === 1;
          renderBlocks();
        }),
      ]),
      el.blocksMessage,
      h("div", { class: "nw-scroll" }, [
        h("table", { class: "nw-blocks" }, [
          h("thead", null, [h("tr", null, [
            head("Block"),
            head("When"),
            head("Miner"),
            head("Transactions", true),
            head("Reward", true),
          ])]),
          el.blocks,
        ]),
      ]),
      el.blocksUpdated.root,
    ]);
  }

  function emptyRow(text) {
    return h("tr", null, [h("td", { class: "nw-empty", colSpan: 5, text: text })]);
  }

  function blockRow(b, now) {
    var miner = b.own
      ? h("td", null, [h("span", { class: "nw-you" }, [ghost(), "You"])])
      : h("td", { class: "mono nw-quiet", title: b.miner, text: show(b.minerShort) });
    var reward = "–";
    if (b.own && view.rewards[b.number] !== undefined) reward = "+" + view.rewards[b.number];
    return h("tr", { class: b.own ? "nw-own" : "" }, [
      h("td", { class: "mono", text: show(b.number, f.num) }),
      h("td", { class: "nw-quiet", text: show(b.time, function (t) { return f.ago(now - t); }) }),
      miner,
      h("td", { class: "mono nw-quiet nw-right", text: show(b.transactions, f.num) }),
      h("td", { class: "mono nw-right", text: reward }),
    ]);
  }

  function renderBlocks() {
    var s = view.network;
    D.clear(el.blocks);
    if (!s) {
      el.blocks.appendChild(emptyRow("Checking the latest blocks…"));
      return;
    }
    if (!s.running) {
      el.blocks.appendChild(emptyRow("Start the node to see the latest blocks."));
      return;
    }
    var blocks = (s.blocks || []).filter(function (b) { return !view.yoursOnly || b.own; });
    if (blocks.length === 0) {
      el.blocks.appendChild(emptyRow(view.yoursOnly
        ? "None of the latest 20 blocks are yours."
        : "The node has no blocks to show yet."));
      return;
    }
    var now = Date.now() / 1000;
    blocks.forEach(function (b) { el.blocks.appendChild(blockRow(b, now)); });
  }

  function onNetwork(s) {
    view.network = s;
    setMessage(el.blocksMessage, "");
    renderStats(s);
    renderBlocks();
    markUpdated(el.blocksUpdated.stamp);
  }

  function onNetworkError(err) {
    setMessage(el.blocksMessage, err.message);
  }

  function onRewards(s) {
    var rewards = {};
    (s.blocks || []).forEach(function (b) {
      if (b.reward && b.reward.coins !== undefined && b.reward.coins !== null) rewards[b.number] = b.reward.coins;
    });
    view.rewards = rewards;
    renderBlocks();
  }

  // Node log.

  function logCard() {
    el.logMessage = h("p", { class: "message", id: "nw-log-message", hidden: true });
    el.logNote = h("p", { class: "nw-log-note", id: "nw-log-note", hidden: true });
    el.log = h("ol", { class: "nw-log", id: "nw-log-lines", hidden: true });
    el.logUpdated = updatedLine();
    return h("section", { class: "card nw-card", id: "nw-log-card", "aria-labelledby": "nw-log-title" }, [
      h("div", { class: "nw-head" }, [
        h("div", null, [
          h("h2", { id: "nw-log-title", text: "Node log" }),
          h("p", { class: "nw-sub", text: "The last lines the node wrote, with common ones explained." }),
        ]),
        segmented("How to show the log", ["Explained", "Raw"], function (i) {
          view.raw = i === 1;
          renderLog();
        }),
      ]),
      el.logMessage,
      el.logNote,
      el.log,
      el.logUpdated.root,
    ]);
  }

  function logLine(line) {
    if (view.raw) {
      return h("li", null, [h("span", { class: "nw-raw mono", text: line.text })]);
    }
    var parts = splitLog(line.text);
    return h("li", null, [
      h("span", { class: "nw-time mono", text: show(parts.time) }),
      line.meaning ? h("span", { class: "nw-meaning", text: line.meaning }) : null,
      h("span", { class: "nw-raw mono", text: parts.raw }),
    ]);
  }

  function renderLog() {
    var s = view.log;
    D.clear(el.log);
    el.log.className = view.raw ? "nw-log nw-log-raw" : "nw-log";
    if (!s) return;
    if (!s.available) {
      el.log.hidden = true;
      setMessage(el.logNote, s.message || "There is no node log to read.");
      return;
    }
    var lines = s.lines || [];
    if (lines.length === 0) {
      el.log.hidden = true;
      setMessage(el.logNote, "The node has not written anything yet.");
      return;
    }
    setMessage(el.logNote, "");
    el.log.hidden = false;
    lines.forEach(function (line) { el.log.appendChild(logLine(line)); });
  }

  function onLog(s) {
    view.log = s;
    setMessage(el.logMessage, "");
    renderLog();
    markUpdated(el.logUpdated.stamp);
  }

  function onLogError(err) {
    setMessage(el.logMessage, err.message);
  }

  // The sentence follows the node's sync state.
  function renderSync(s) {
    if (!s.running) {
      screen.setSentence("The node is stopped, so it is not following the network.");
    } else if (s.syncing === true) {
      screen.setSentence("Catching up with the network: block " + show(s.currentBlock, f.num) +
        " of " + show(s.highestBlock, f.num) + ", " + show(s.peers) + " peers.");
    } else if (s.syncing === false) {
      screen.setSentence("Your node is in sync with the rest of the " +
        (s.networkName ? s.networkName : "test network") + ".");
    } else {
      screen.setSentence("The node does not say whether it is in sync.");
    }
  }

  D.screen("network", {
    title: "Network",
    sentence: "Checking the network…",
    mount: function (root, s) {
      screen = s;
      loadStyles();
      root.appendChild(statsRow());
      root.appendChild(blocksCard());
      root.appendChild(logCard());
      renderBlocks();
      D.poll("/api/network", onNetwork, onNetworkError);
      D.poll("/api/rewards", onRewards);
      D.poll("/api/log", onLog, onLogError);
      D.poll("/api/node", renderSync);
    },
  });
})(GhostDash);
