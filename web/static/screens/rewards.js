"use strict";

// Rewards: blocks won, what they earned, the payout balance and lost races.
// All of this screen's markup is built here.
(function (D) {
  var h = D.h;
  var f = D.format;
  var show = f.show;

  function text(id, value) {
    document.getElementById(id).textContent = value;
  }

  var screen = null;

  function rewardsCard() {
    var c = D.card({ id: "rewards-panel", title: "Rewards" });
    c.field("Blocks won today", "rewards-today-count", { mono: true });
    c.field("Earned today", "rewards-today", { mono: true });
    c.field("Blocks won in total", "rewards-total-count", { mono: true });
    c.field("Earned in total", "rewards-total", { mono: true });
    c.field("Payout balance", "rewards-balance", { mono: true });
    c.field("Lost races", "rewards-lost", { mono: true });
    c.body.appendChild(h("p", { class: "note", id: "rewards-lost-note" }));
    c.body.appendChild(h("h3", { text: "Blocks won" }));
    c.body.appendChild(h("div", { class: "table-wrap" }, [
      h("table", { class: "blocks" }, [
        h("thead", null, [h("tr", null, [
          h("th", { scope: "col", text: "Block" }),
          h("th", { scope: "col", text: "Time" }),
          h("th", { scope: "col", text: "Reward" }),
        ])]),
        h("tbody", { id: "rewards-blocks" }),
      ]),
    ]));
    c.body.appendChild(h("p", { class: "note", id: "rewards-note" }));
    return c;
  }

  function renderRewards(panel, s) {
    if (s.running) {
      panel.setState(s.indexing ? "Reading blocks" : "Up to date", s.indexing ? "wait" : "ok");
    }
    if (s.payoutAddress) screen.setSentence("Every block reward goes to " + s.payoutAddress);
    else if (!s.running) screen.setSentence("The node is stopped, so its payout address cannot be read.");
    else screen.setSentence("The node has no payout address, so it cannot win blocks. Set one in Settings.");
    text("rewards-today-count", show(s.wonToday, f.num));
    text("rewards-today", f.coins(s.rewardToday));
    text("rewards-total-count", show(s.wonTotal, f.num));
    text("rewards-total", f.coins(s.rewardTotal));
    text("rewards-balance", show(s.balance, f.coins));
    text("rewards-lost", show(s.lostRaces, f.num));
    text("rewards-lost-note", "A lost race: " + s.lostRacesNote);

    var body = document.getElementById("rewards-blocks");
    D.clear(body);
    (s.blocks || []).forEach(function (b) {
      var row = h("tr");
      [f.num(b.number), new Date(b.time * 1000).toLocaleString(), b.reward ? f.coins(b.reward) : "unknown"]
        .forEach(function (v, i) {
          row.appendChild(h("td", { text: v, class: i !== 1 ? "mono" : "", title: i === 0 ? b.hash : undefined }));
        });
      body.appendChild(row);
    });

    var notes = [];
    if (!s.payoutAddress) notes.push("The node has no payout address, so it cannot win blocks.");
    else if (!s.blocks || !s.blocks.length) notes.push("No blocks won yet.");
    if (s.wonTotal > (s.blocks || []).length) notes.push("Showing the latest " + s.blocks.length + " blocks won.");
    if (s.indexing) {
      notes.push("Reading the chain for your blocks: " +
        (s.indexing.readTo === null ? "starting" : "block " + f.num(s.indexing.readTo)) +
        " of " + f.num(s.indexing.height) + ".");
    }
    if (s.unknownRewards) {
      notes.push(s.unknownRewards + " block(s) won show no reward: the node no longer keeps the balances from then, so they are left out of the sums.");
    }
    notes.push("Each reward is your balance change across the block. Today is in this computer's time.");
    if (s.indexError) notes.push("Index problem: " + s.indexError);
    text("rewards-note", notes.join(" "));
  }

  D.screen("rewards", {
    title: "Rewards",
    sentence: "Checking where block rewards go…",
    mount: function (root, s) {
      screen = s;
      var c = rewardsCard();
      root.appendChild(c.root);
      D.panel(c, "/api/rewards", renderRewards);
    },
  });
})(GhostDash);
