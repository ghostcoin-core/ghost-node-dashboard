"use strict";

// Network: the network panel with the latest blocks, and the node log.
// All of this screen's markup is built here.
(function (D) {
  var h = D.h;
  var f = D.format;
  var show = f.show;

  function text(id, value) {
    document.getElementById(id).textContent = value;
  }

  var screen = null;

  function networkCard() {
    var c = D.card({ id: "network-panel", title: "Network" });
    c.field("Height", "network-height", { mono: true });
    c.field("Average block time", "network-blocktime");
    c.field("Difficulty", "network-difficulty", { mono: true });
    c.field("Network hash rate (estimate)", "network-hashrate", { mono: true });
    c.body.appendChild(h("h3", { text: "Latest blocks" }));
    c.body.appendChild(h("div", { class: "table-wrap" }, [
      h("table", { class: "blocks" }, [
        h("thead", null, [h("tr", null, [
          h("th", { scope: "col", text: "Block" }),
          h("th", { scope: "col", text: "Time" }),
          h("th", { scope: "col", text: "Miner" }),
        ])]),
        h("tbody", { id: "network-blocks" }),
      ]),
    ]));
    c.body.appendChild(h("p", { class: "note", id: "network-note" }));
    return c;
  }

  function renderNetwork(panel, s) {
    if (s.running) panel.setState("Connected", "ok");
    text("network-height", show(s.height, f.num));
    text("network-blocktime", show(s.averageBlockSeconds, function (secs) {
      return secs.toFixed(1) + " s (last " + s.averageOverBlocks + " blocks)";
    }));
    text("network-difficulty", show(s.difficulty, f.num));
    text("network-hashrate", show(s.networkHashRate, f.rate));
    text("network-note", "The network hash rate is an estimate: difficulty ÷ the " +
      s.targetBlockSeconds + " s target block time. Your own blocks are highlighted.");

    var body = document.getElementById("network-blocks");
    D.clear(body);
    (s.blocks || []).forEach(function (b) {
      var row = h("tr", { class: b.own ? "own" : "" });
      [f.num(b.number), new Date(b.time * 1000).toLocaleTimeString(), b.minerShort + (b.own ? " (you)" : "")]
        .forEach(function (v, i) {
          row.appendChild(h("td", { text: v, class: i !== 1 ? "mono" : "", title: i === 2 ? b.miner : undefined }));
        });
      body.appendChild(row);
    });
  }

  // The sentence follows the node's sync state.
  function renderSync(s) {
    if (!s.running) {
      screen.setSentence("The node is stopped, so it is not following the network.");
    } else if (s.syncing === true) {
      screen.setSentence("Catching up with the network: block " + show(s.currentBlock, f.num) +
        " of " + show(s.highestBlock, f.num) + ", " + show(s.peers) + " peers.");
    } else if (s.syncing === false) {
      screen.setSentence("In sync with the network at block " + show(s.currentBlock, f.num) +
        ", " + show(s.peers) + " peers.");
    } else {
      screen.setSentence("The node does not say whether it is in sync.");
    }
  }

  function logCard() {
    var c = D.card({ id: "log-panel", title: "Node log" });
    c.body.appendChild(h("p", { class: "note", id: "log-note" }));
    c.body.appendChild(h("ol", { class: "log", id: "log-lines" }));
    return c;
  }

  function renderLog(panel, s) {
    if (s.available) panel.setState("Read only", "ok");
    else panel.setState("No log", "wait");
    text("log-note", s.available
      ? "The last " + s.lines.length + " lines of " + s.path + ", newest first."
      : s.message + " (" + s.path + ")");
    var list = document.getElementById("log-lines");
    D.clear(list);
    s.lines.forEach(function (line) {
      list.appendChild(h("li", null, [
        h("code", { text: line.text }),
        line.meaning ? h("span", { class: "meaning", text: line.meaning }) : null,
      ]));
    });
  }

  D.screen("network", {
    title: "Network",
    sentence: "Checking the network…",
    mount: function (root, s) {
      screen = s;
      var net = networkCard();
      var log = logCard();
      root.appendChild(net.root);
      root.appendChild(log.root);
      D.panel(net, "/api/network", renderNetwork);
      D.panel(log, "/api/log", renderLog, true);
      D.poll("/api/node", renderSync);
    },
  });
})(GhostDash);
