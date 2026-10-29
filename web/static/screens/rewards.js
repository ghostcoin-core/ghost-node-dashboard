"use strict";

// Rewards: the totals, blocks won per day, every block won page by page and
// the lost races. All of this screen's markup is built here; its look is in
// rewards.css, which this file links once when the screen mounts.
(function (D) {
  var h = D.h;
  var f = D.format;
  var show = f.show;

  var CSS_HREF = "/static/screens/rewards.css";
  var PAGE_SIZE = 8;
  var DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  var LOST_NOTE = "Sometimes two nodes find the same block at almost the same moment. " +
    "Only one can be kept, so the other is dropped. It's normal, and nothing is taken from you.";

  var screen = null;
  var el = {};
  var last = null; // the latest /api/rewards answer
  var mining = null; // the latest /api/mining answer
  var range = "7"; // "7", "30" or "all"
  var cursors = [null]; // the before of every page shown so far; the last is the page shown
  var pageToken = 0;

  function linkStylesheet() {
    var links = document.head.querySelectorAll('link[rel="stylesheet"]');
    for (var i = 0; i < links.length; i++) {
      if (links[i].getAttribute("href") === CSS_HREF) return;
    }
    document.head.appendChild(h("link", { rel: "stylesheet", href: CSS_HREF }));
  }

  // Times.

  function pad(n) {
    return n < 10 ? "0" + n : String(n);
  }

  function startOfDay(d) {
    return new Date(d.getFullYear(), d.getMonth(), d.getDate());
  }

  function dayMonth(d) {
    return d.getDate() + " " + MONTHS[d.getMonth()];
  }

  // when says when a block came: "Today 13:36", "Yesterday 22:10",
  // "Mon 09:02" within a week, else the date.
  function when(unix) {
    if (unix === null || unix === undefined) return "–";
    var d = new Date(unix * 1000);
    var now = new Date();
    var days = Math.round((startOfDay(now) - startOfDay(d)) / 86400000);
    var hm = pad(d.getHours()) + ":" + pad(d.getMinutes());
    if (days <= 0) return "Today " + hm;
    if (days === 1) return "Yesterday " + hm;
    if (days < 7) return DAYS[d.getDay()] + " " + hm;
    return dayMonth(d) + (d.getFullYear() === now.getFullYear() ? "" : " " + d.getFullYear());
  }

  // parseDay reads a "YYYY-MM-DD" local day.
  function parseDay(s) {
    var p = String(s).split("-");
    return new Date(Number(p[0]), Number(p[1]) - 1, Number(p[2]));
  }

  // Building.

  function numberCard(label, id) {
    return h("div", { class: "rw-stat" }, [
      h("p", { class: "rw-stat-label", text: label }),
      h("p", { class: "rw-stat-value" }, [
        h("span", { class: "mono", id: id, text: "–" }),
        " ",
        h("span", { class: "rw-unit", text: "GHOST" }),
      ]),
      h("p", { class: "rw-stat-hint", id: id + "-hint", text: "–" }),
    ]);
  }

  function rangeButton(value, label) {
    var b = h("button", { type: "button", class: "rw-seg", text: label, "aria-pressed": value === range ? "true" : "false" });
    b.dataset.range = value;
    b.addEventListener("click", function () {
      range = value;
      Array.prototype.forEach.call(el.switch.querySelectorAll("button"), function (x) {
        x.setAttribute("aria-pressed", x.dataset.range === range ? "true" : "false");
      });
      renderChart();
    });
    return b;
  }

  function copyAddressButton() {
    var b = D.copyButton("rw-copy");
    b.textContent = "Copy address";
    b.setAttribute("aria-label", "Copy the payout address");
    // copyButton puts its own label back after "Copied"; keep this one's.
    new MutationObserver(function () {
      if (b.textContent === "Copy") b.textContent = "Copy address";
    }).observe(b, { childList: true, characterData: true, subtree: true });
    return b;
  }

  function build(root) {
    el.copy = copyAddressButton();
    el.message = h("p", { class: "message", id: "rw-message", role: "status", hidden: true });

    el.indexText = h("p", { class: "rw-index-text", id: "rw-index-text" });
    el.indexBar = h("progress", { id: "rw-index-progress", "aria-labelledby": "rw-index-text" });
    el.indexing = h("section", { class: "rw-indexing", id: "rw-indexing", hidden: true }, [el.indexText, el.indexBar]);

    el.switch = h("div", { class: "rw-switch", role: "group", "aria-label": "Days shown" }, [
      rangeButton("7", "7 days"),
      rangeButton("30", "30 days"),
      rangeButton("all", "All"),
    ]);
    el.plot = h("div", { class: "rw-plot", id: "rw-plot", role: "img" });
    el.labels = h("div", { class: "rw-labels", id: "rw-labels", "aria-hidden": "true" });
    el.plotScroll = h("div", { class: "rw-plot-scroll" }, [el.plot, el.labels]);
    el.chartEmpty = h("p", { class: "rw-empty", id: "rw-chart-empty", hidden: true });

    el.tbody = h("tbody", { id: "rw-blocks" });
    el.tableWrap = h("div", { class: "rw-scroll" }, [
      h("table", { class: "rw-table" }, [
        h("thead", null, [h("tr", null, [
          h("th", { scope: "col", text: "Block" }),
          h("th", { scope: "col", text: "When" }),
          h("th", { scope: "col", class: "rw-num", text: "Reward" }),
        ])]),
        el.tbody,
      ]),
    ]);
    el.caption = h("p", { class: "rw-caption", id: "rw-caption", "aria-live": "polite" });
    el.newer = h("button", { type: "button", class: "rw-page", id: "rw-newer", text: "Newer", disabled: true });
    el.older = h("button", { type: "button", class: "rw-page", id: "rw-older", text: "Older", disabled: true });
    el.pager = h("div", { class: "rw-pager" }, [el.caption, h("div", { class: "rw-page-buttons" }, [el.newer, el.older])]);
    el.tableEmpty = h("p", { class: "rw-empty", id: "rw-table-empty", hidden: true });
    el.tableNote = h("p", { class: "note", id: "rw-table-note", hidden: true });

    el.newer.addEventListener("click", function () {
      if (cursors.length > 1) cursors.pop();
      loadPage();
    });
    el.older.addEventListener("click", function () {
      if (el.older.dataset.before === undefined) return;
      cursors.push(Number(el.older.dataset.before));
      loadPage();
    });

    el.lostCount = h("span", { class: "rw-lost-count mono", id: "rw-lost-count", text: "–" });
    el.lostList = h("ul", { class: "rw-lost-list", id: "rw-lost-list" });
    el.lostEmpty = h("p", { class: "rw-lost-none", id: "rw-lost-none", hidden: true });

    el.stamp = h("span", { class: "mono", text: "never" });

    root.appendChild(h("div", { class: "rw-top" }, [el.copy]));
    root.appendChild(el.message);
    root.appendChild(el.indexing);
    root.appendChild(h("div", { class: "rw-stats" }, [
      numberCard("Earned in total", "rw-total"),
      numberCard("Today", "rw-today"),
      numberCard("Last 7 days", "rw-week"),
      numberCard("Payout balance", "rw-balance"),
    ]));
    root.appendChild(h("section", { class: "card rw-chart", "aria-labelledby": "rw-chart-title" }, [
      h("div", { class: "rw-chart-head" }, [h("h2", { id: "rw-chart-title", text: "Blocks won per day" }), el.switch]),
      el.plotScroll,
      el.chartEmpty,
    ]));
    root.appendChild(h("div", { class: "rw-row" }, [
      h("section", { class: "card rw-blocks-card", "aria-labelledby": "rw-blocks-title" }, [
        h("h2", { id: "rw-blocks-title", text: "All blocks won" }),
        el.tableEmpty,
        el.tableWrap,
        el.pager,
        el.tableNote,
      ]),
      h("section", { class: "card rw-lost", "aria-labelledby": "rw-lost-title" }, [
        h("h2", { id: "rw-lost-title" }, ["Lost races ", el.lostCount]),
        h("p", { class: "rw-lost-note", text: LOST_NOTE }),
        el.lostEmpty,
        el.lostList,
      ]),
    ]));
    root.appendChild(h("p", { class: "updated" }, ["Updated ", el.stamp]));
  }

  // Rendering.

  function setText(id, value) {
    document.getElementById(id).textContent = value;
  }

  function coins(a) {
    return a && a.coins !== undefined && a.coins !== null ? a.coins : "–";
  }

  function setMessage(msg, isError) {
    el.message.textContent = msg || "";
    el.message.className = isError ? "message error" : "message";
    el.message.hidden = !msg;
  }

  function isEmpty(s) {
    return !s.indexing && s.wonTotal === 0;
  }

  function renderSentence(s) {
    var addr = s.payoutAddress;
    if (!s.running && addr) screen.setSentence("The node is stopped. Every block reward goes to " + addr);
    else if (addr) screen.setSentence("Every block reward goes to " + addr);
    else if (!s.running) screen.setSentence("The node is stopped, so its payout address cannot be read.");
    else screen.setSentence("The node has no payout address, so it cannot win blocks. Set one in Settings.");
    el.copy.hidden = !addr;
    if (addr) el.copy.dataset.value = addr;
  }

  function renderIndexing(s) {
    var ix = s.indexing;
    el.indexing.hidden = !ix;
    if (!ix) return;
    el.indexBar.max = Math.max(ix.height, 1);
    if (ix.readTo === null || ix.readTo === undefined) {
      el.indexText.textContent = "Reading blocks: starting, " + f.num(ix.height) + " blocks to read. " +
        "The totals show once every block has been read.";
      el.indexBar.removeAttribute("value");
    } else {
      el.indexText.textContent = "Reading blocks: block " + f.num(ix.readTo) + " of " + f.num(ix.height) +
        ". The totals show once every block has been read.";
      el.indexBar.value = Math.min(ix.readTo, ix.height);
    }
  }

  function renderTotals(s) {
    var reading = !!s.indexing;
    setText("rw-total", reading ? "–" : coins(s.rewardTotal));
    setText("rw-today", reading ? "–" : coins(s.rewardToday));
    setText("rw-week", reading || !s.last7 ? "–" : coins(s.last7.reward));
    setText("rw-balance", show(s.balance, coins));
    if (reading) {
      setText("rw-total-hint", "reading blocks…");
      setText("rw-today-hint", "reading blocks…");
      setText("rw-week-hint", "reading blocks…");
    } else {
      setText("rw-total-hint", f.num(s.wonTotal) + (s.wonTotal === 1 ? " block won" : " blocks won"));
      setText("rw-today-hint", f.num(s.wonToday) + (s.wonToday === 1 ? " block so far" : " blocks so far"));
      setText("rw-week-hint", s.last7 ? "about " + (s.last7.won / 7).toFixed(1) + " blocks a day" : "–");
    }
    setText("rw-balance-hint", s.balance ? "on the address now" : "the node is needed to read it");
  }

  function chartDays(s) {
    var d = s.daily || {};
    if (range === "30") return d.days30 || [];
    if (range === "all") return d.all || [];
    return d.days7 || [];
  }

  function chartLabel(days) {
    var counts = days.map(function (x) { return x.won; });
    var head;
    if (range === "7") head = "Blocks won per day over the last 7 days: ";
    else if (range === "30") head = "Blocks won per day over the last 30 days: ";
    else head = "Blocks won per day since " + dayMonth(parseDay(days[0].day)) + " " + parseDay(days[0].day).getFullYear() + ": ";
    if (counts.length === 1) return head + counts[0] + " so far today";
    var todayCount = counts.pop();
    return head + counts.join(", ") + ", and " + todayCount + " so far today";
  }

  function dayLabel(day, isToday) {
    if (isToday) return "Today";
    var d = parseDay(day);
    return range === "7" ? DAYS[d.getDay()] : dayMonth(d);
  }

  function renderChart() {
    var s = last;
    D.clear(el.plot);
    D.clear(el.labels);
    if (!s) return;
    var days = chartDays(s);
    var empty = isEmpty(s);
    var reading = !!s.indexing;
    el.chartEmpty.hidden = !(empty || reading || !days.length);
    el.plot.hidden = !el.chartEmpty.hidden;
    el.plotScroll.hidden = el.plot.hidden;
    if (empty || !days.length) el.chartEmpty.textContent = "No blocks won yet.";
    if (reading) el.chartEmpty.textContent = "Blocks won per day show once every block has been read.";
    if (el.plot.hidden) {
      el.plot.removeAttribute("aria-label");
      return;
    }

    var n = days.length;
    var max = 1;
    days.forEach(function (x) { if (x.won > max) max = x.won; });
    var columns = "repeat(" + n + ", minmax(0, 1fr))";
    el.plot.style.gridTemplateColumns = columns;
    el.labels.style.gridTemplateColumns = columns;
    el.plot.className = range === "7" ? "rw-plot" : "rw-plot rw-plot-long";
    el.labels.className = range === "7" ? "rw-labels" : "rw-labels rw-labels-long";
    el.plot.setAttribute("aria-label", chartLabel(days));

    // The gap between bars shrinks as the days grow so the gaps never eat the
    // bars; below MIN_COLUMN px a day the plot keeps that width and scrolls
    // sideways inside its card.
    var MIN_COLUMN = 4;
    var width = el.plotScroll.clientWidth || 640;
    var gap = range === "7" ? 16 : Math.max(0, Math.min(4, Math.floor(width / n / 4)));
    var minWidth = "";
    if (width / n < MIN_COLUMN + gap) {
      gap = 1;
      minWidth = n * (MIN_COLUMN + gap) + "px";
      width = n * (MIN_COLUMN + gap);
    }
    el.plot.style.columnGap = gap + "px";
    el.labels.style.columnGap = gap + "px";
    el.plot.style.minWidth = minWidth;
    el.labels.style.minWidth = minWidth;

    // Thin the day labels (and the counts on narrow columns) so they do not
    // overlap; the last label, today, is always kept.
    var step = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(width / 56))));
    var dense = width / n < 22;
    if (dense) el.plot.className += " rw-plot-dense";

    days.forEach(function (x, i) {
      var isToday = i === n - 1;
      var bar = h("div", { class: isToday ? "rw-bar rw-bar-today" : "rw-bar" });
      bar.style.height = "calc((100% - 26px) * " + (x.won / max).toFixed(4) + ")";
      el.plot.appendChild(h("div", { class: "rw-col" }, [
        h("span", { class: "rw-count mono", text: String(x.won) }),
        bar,
      ]));
      var shown = (n - 1 - i) % step === 0;
      el.labels.appendChild(h("span", { class: "rw-day", text: shown ? dayLabel(x.day, isToday) : "" }));
    });
  }

  function emptyText(s) {
    if (!s.payoutAddress && s.running) return "No payout address is set, so no block can be won. Set one in Settings.";
    if (!s.running) return "No blocks won yet. The node is stopped, so it is not mining.";
    if (!mining || mining.paused) return mining ? "Mining is paused." : "Mining is on.";
    var secs = mining.expectedBlockSeconds;
    if (secs === null || secs === undefined) return "Mining is on.";
    return "Mining is on. A first block can take a while; at this share of the network it is expected in about " +
      f.duration(secs) + ".";
  }

  function renderTableState() {
    var s = last;
    if (!s) return;
    var empty = isEmpty(s);
    el.tableEmpty.hidden = !empty;
    el.tableWrap.hidden = empty;
    el.pager.hidden = empty;
    if (empty) el.tableEmpty.textContent = emptyText(s);
    var notes = [];
    if (s.unknownRewards) {
      notes.push(f.num(s.unknownRewards) + (s.unknownRewards === 1 ? " block won shows" : " blocks won show") +
        " no reward: the node no longer keeps the balances from then, so they are left out of the sums.");
    }
    if (!empty) notes.push("Each reward is your balance change across the block. Days are in this computer's time.");
    el.tableNote.textContent = notes.join(" ");
    el.tableNote.hidden = !notes.length;
  }

  function renderPage(page) {
    var rows = page.blocks || [];
    D.clear(el.tbody);
    rows.forEach(function (b) {
      el.tbody.appendChild(h("tr", null, [
        h("td", { class: "mono", text: f.num(b.number), title: b.hash || undefined }),
        h("td", { class: "rw-when", text: when(b.time) }),
        h("td", { class: b.reward ? "mono rw-num" : "rw-num rw-unknown", text: b.reward ? "+" + b.reward.coins : "unknown" }),
      ]));
    });
    var first = (cursors.length - 1) * PAGE_SIZE;
    var total = last && !last.indexing ? f.num(last.wonTotal) : null;
    if (!rows.length) el.caption.textContent = total === null ? "None found yet" : "0 of " + total;
    else if (total === null) el.caption.textContent = (first + 1) + "–" + (first + rows.length) + " found so far";
    else el.caption.textContent = (first + 1) + "–" + (first + rows.length) + " of " + total;
    el.newer.disabled = cursors.length <= 1;
    var next = page.nextBefore;
    el.older.disabled = next === null || next === undefined;
    if (el.older.disabled) delete el.older.dataset.before;
    else el.older.dataset.before = String(next);
  }

  function pageURL(before) {
    var url = "/api/rewards?";
    if (before !== null && before !== undefined) url += "before=" + before + "&";
    return url + "limit=" + PAGE_SIZE;
  }

  function loadPage() {
    var token = ++pageToken;
    var before = cursors[cursors.length - 1];
    D.getJSON(pageURL(before)).then(function (page) {
      if (token !== pageToken) return;
      // The index can shrink (a new payout address): go back to page one.
      if (cursors.length > 1 && !(page.blocks || []).length) {
        cursors = [null];
        loadPage();
        return;
      }
      renderPage(page);
    }).catch(function (err) {
      if (token !== pageToken) return;
      setMessage(err.message, true);
    });
  }

  function renderLost(s) {
    el.lostCount.textContent = show(s.lostRaces, f.num);
    D.clear(el.lostList);
    var list = s.lostRaceBlocks;
    if (list === null || list === undefined) {
      el.lostEmpty.textContent = "The node log cannot be read, so lost races are not known.";
      el.lostEmpty.hidden = false;
      return;
    }
    el.lostEmpty.textContent = "None so far.";
    el.lostEmpty.hidden = !(s.lostRaces === 0 || (s.lostRaces === null && !list.length));
    list.forEach(function (r) {
      el.lostList.appendChild(h("li", null, [
        h("span", { class: "mono", text: f.num(r.number) }),
        h("span", { class: "rw-lost-time", text: when(r.time) }),
      ]));
    });
  }

  function onData(s) {
    last = s;
    renderSentence(s);
    if (s.indexError) setMessage("Index problem: " + s.indexError, true);
    else setMessage("");
    renderIndexing(s);
    renderTotals(s);
    renderChart();
    renderTableState();
    renderLost(s);
    el.stamp.dataset.at = String(Date.now());
    el.stamp.textContent = f.ago(0);
    if (!isEmpty(s)) loadPage();
  }

  function onError(err) {
    setMessage(err.message, true);
  }

  D.screen("rewards", {
    title: "Rewards",
    sentence: "Checking where block rewards go…",
    mount: function (root, s) {
      screen = s;
      linkStylesheet();
      build(root);
      D.poll("/api/rewards", onData, onError);
      D.poll("/api/mining", function (m) {
        mining = m;
        renderTableState();
      });
      var resizeTimer = null;
      window.addEventListener("resize", function () {
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(renderChart, 150);
      });
      window.addEventListener("hashchange", function () {
        if (location.hash === "#/rewards") setTimeout(renderChart, 0);
      });
    },
  });
})(GhostDash);
