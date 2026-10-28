"use strict";

// GhostDash is the shared core of the dashboard page. index.html loads this
// file first, then one file per screen from /static/screens/, all with defer,
// so every screen file can call GhostDash at once and the page is built when
// the document has been read.
//
// The contract (GhostDash is the only global this file defines):
//
//   GhostDash.REFRESH_MS             5000: how often polled data refreshes.
//   GhostDash.getJSON(url)           GET, same-origin, cache "no-store".
//                                    Resolves to the decoded JSON; rejects
//                                    with an Error whose message can be shown.
//   GhostDash.postJSON(url, body)    POST a JSON body, same-origin, so the
//                                    browser sends the Origin header the
//                                    server checks. Resolves to the JSON answer.
//   GhostDash.poll(url, onData, onError)
//                                    Fetch url now and every REFRESH_MS.
//                                    Callers of the same url share one request.
//   GhostDash.refresh()              Fetch every polled url now.
//   GhostDash.format                 num, rate (hash rate), duration, percent,
//                                    coins, ago ("N s ago"), shortAddress and
//                                    show (a dash for null, else format(v)).
//   GhostDash.h(tag, attrs, kids)    Build an element. attrs: "class", "text",
//                                    properties or attributes; kids: nodes or
//                                    strings, added as text, never as HTML.
//   GhostDash.clear(node)            Remove every child of node.
//   GhostDash.card({id, title})      A white card with a title, a state pill,
//                                    a message line and "Updated N s ago".
//                                    Returns {root, body, setState(label, kind),
//                                    setMessage(text), field(label, id, opts),
//                                    markUpdated()}; kind is ok, wait or bad.
//   GhostDash.panel(card, url, render, local)
//                                    Poll url into card: shows "Node not
//                                    running" or the error on the card, then
//                                    calls render(card, data). A local panel
//                                    reads this computer, not the node.
//   GhostDash.copyButton(id)         A Copy button that copies its data-value.
//   GhostDash.screen(name, {title, sentence, mount})
//                                    Register the screen for the route
//                                    #/<name>. mount(root, screen) builds all
//                                    of the screen's DOM inside root, once;
//                                    screen.setSentence(text) changes the
//                                    sentence under the page title.
//
// The core also owns the sidebar: the active nav item, the Rewards badge
// (wonTotal from /api/rewards) and the node card (from /api/node).
var GhostDash = (function () {
  var REFRESH_MS = 5000;
  var NOT_SIGNED_IN = "This browser is not signed in. Open the dashboard from the link the installer printed.";
  var NOT_RUNNING = "Node not running. Start it and this panel will update by itself.";
  var ROUTES = ["overview", "rewards", "network", "settings"];

  // Formatting.

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

  function ago(secs) {
    secs = Math.max(0, Math.floor(secs));
    if (secs < 60) return secs + " s ago";
    if (secs < 3600) return Math.floor(secs / 60) + " min ago";
    return Math.floor(secs / 3600) + " h ago";
  }

  function shortAddress(a) {
    if (!a || a.length <= 14) return a || "";
    return a.slice(0, 6) + "…" + a.slice(-4);
  }

  // Requests.

  function getJSON(url) {
    return fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.status === 401) throw new Error(NOT_SIGNED_IN);
      if (!r.ok) throw new Error("The dashboard answered " + r.status + ".");
      return r.json();
    });
  }

  function postJSON(url, body) {
    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    }).then(function (r) {
      if (r.status === 401) throw new Error(NOT_SIGNED_IN);
      return r.json().catch(function () {
        throw new Error("The dashboard answered " + r.status + ".");
      });
    });
  }

  // Polling. Each url is fetched once per round and handed to every caller.

  var polls = {};
  var pollOrder = [];

  function poll(url, onData, onError) {
    if (!polls[url]) {
      polls[url] = [];
      pollOrder.push(url);
    }
    polls[url].push({ data: onData, error: onError });
    if (started) fetchOne(url);
  }

  function fetchOne(url) {
    getJSON(url)
      .then(function (data) {
        polls[url].forEach(function (s) { s.data(data); });
      })
      .catch(function (err) {
        polls[url].forEach(function (s) { if (s.error) s.error(err); });
      });
  }

  function refresh() {
    pollOrder.forEach(fetchOne);
  }

  // DOM building.

  function h(tag, attrs, kids) {
    var node = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (key) {
      var v = attrs[key];
      if (v === undefined || v === null) return;
      if (key === "class") node.className = v;
      else if (key === "text") node.textContent = v;
      else if (key.indexOf("-") >= 0 || !(key in node)) node.setAttribute(key, v);
      else node[key] = v;
    });
    (kids || []).forEach(function (kid) {
      if (kid === null || kid === undefined) return;
      node.appendChild(typeof kid === "string" ? document.createTextNode(kid) : kid);
    });
    return node;
  }

  function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  // Cards.

  function Card(opts) {
    var titleId = opts.id + "-title";
    this.state = h("span", { class: "pill pill-wait", text: "Checking…" });
    this.message = h("p", { class: "message", hidden: true });
    this.stamp = h("span", { class: "mono", text: "never" });
    this.body = h("div", { class: "card-body" });
    this.root = h("section", { class: "card", id: opts.id, "aria-labelledby": titleId }, [
      h("div", { class: "card-head" }, [h("h2", { id: titleId, text: opts.title }), this.state]),
      this.message,
      this.body,
      h("p", { class: "updated" }, ["Updated ", this.stamp]),
    ]);
  }

  Card.prototype.setState = function (label, kind) {
    this.state.textContent = label;
    this.state.className = "pill pill-" + kind;
  };

  Card.prototype.setMessage = function (msg) {
    this.message.textContent = msg || "";
    this.message.hidden = !msg;
  };

  Card.prototype.markUpdated = function () {
    this.stamp.dataset.at = String(Date.now());
    this.stamp.textContent = ago(0);
  };

  // field adds one label and value to the card's list of fields (made on
  // first use) and returns the value element.
  Card.prototype.field = function (label, id, opts) {
    opts = opts || {};
    if (!this.fields) {
      this.fields = h("dl", { class: "fields" });
      this.body.appendChild(this.fields);
    }
    var dd = h("dd", { id: id, class: opts.mono ? "mono" : "", text: "–" });
    this.fields.appendChild(h("div", { id: opts.rowId, class: opts.wide ? "wide" : "" }, [h("dt", { text: label }), dd]));
    return dd;
  };

  function card(opts) {
    return new Card(opts);
  }

  function panel(c, url, render, local) {
    poll(url, function (data) {
      if (local || data.running) {
        c.setMessage("");
      } else {
        c.setState("Node not running", "bad");
        c.setMessage(data.error === "node not running" ? NOT_RUNNING : data.error || NOT_RUNNING);
      }
      render(c, data);
      c.markUpdated();
    }, function (err) {
      c.setState("No answer", "bad");
      c.setMessage(err.message);
    });
  }

  function tickUpdated() {
    var now = Date.now();
    Array.prototype.forEach.call(document.querySelectorAll(".updated [data-at]"), function (s) {
      s.textContent = ago((now - Number(s.dataset.at)) / 1000);
    });
  }

  function copyButton(id) {
    var button = h("button", { type: "button", id: id, class: "copy", hidden: true, text: "Copy" });
    button.addEventListener("click", function () {
      var value = button.dataset.value;
      if (!value || !navigator.clipboard) return;
      navigator.clipboard.writeText(value).then(function () {
        button.textContent = "Copied";
        setTimeout(function () { button.textContent = "Copy"; }, 1500);
      });
    });
    return button;
  }

  // Screens and routes.

  var screens = {};
  var current = null;
  var started = false;
  var pageTitle, pageSentence;

  function screen(name, def) {
    screens[name] = { name: name, def: def, sentence: def.sentence || "", root: null };
  }

  function routeName() {
    var m = /^#\/([a-z]+)$/.exec(location.hash);
    return m && ROUTES.indexOf(m[1]) >= 0 && screens[m[1]] ? m[1] : "overview";
  }

  function showRoute(moveFocus) {
    var name = routeName();
    var s = screens[name];
    if (!s) return;
    current = name;
    Object.keys(screens).forEach(function (n) {
      if (screens[n].root) screens[n].root.hidden = n !== name;
    });
    pageTitle.textContent = s.def.title;
    pageSentence.textContent = s.sentence;
    document.title = s.def.title + " · Ghost Node Dashboard";
    Array.prototype.forEach.call(document.querySelectorAll(".nav a[data-route]"), function (a) {
      if (a.dataset.route === name) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
    if (moveFocus) pageTitle.focus();
  }

  function mountScreens(main) {
    pageTitle = h("h1", { id: "page-title", tabIndex: -1 });
    pageSentence = h("p", { id: "page-sentence", class: "sentence" });
    main.appendChild(h("header", { class: "page-head" }, [pageTitle, pageSentence]));
    ROUTES.forEach(function (name) {
      var s = screens[name];
      if (!s) return;
      s.root = h("div", { class: "screen", id: "screen-" + name, hidden: true });
      main.appendChild(s.root);
      s.def.mount(s.root, {
        setSentence: function (text) {
          s.sentence = text;
          if (current === name) pageSentence.textContent = text;
        },
      });
    });
  }

  // Sidebar.

  function text(id, value) {
    document.getElementById(id).textContent = value;
  }

  function watchSidebar() {
    var dot = document.getElementById("side-dot");
    poll("/api/node", function (s) {
      text("side-state", s.running ? "Node running" : "Node stopped");
      dot.className = s.running ? "dot dot-on" : "dot";
      var peers = s.peers === null || s.peers === undefined ? "–" : num(s.peers);
      text("side-network", show(s.networkName, String) + " · " + peers + (s.peers === 1 ? " peer" : " peers"));
      text("side-version", show(s.clientVersion));
      text("side-chain", "Chain ID " + show(s.chainId));
    }, function () {
      text("side-state", "No answer");
      dot.className = "dot";
    });
    var badge = document.getElementById("nav-rewards-badge");
    poll("/api/rewards", function (s) {
      var won = typeof s.wonTotal === "number" ? s.wonTotal : null;
      badge.hidden = won === null;
      badge.textContent = won === null ? "" : num(won);
      badge.setAttribute("aria-label", (won === null ? "" : num(won)) + " blocks won");
    });
  }

  function start() {
    mountScreens(document.getElementById("main"));
    watchSidebar();
    showRoute(false);
    window.addEventListener("hashchange", function () { showRoute(true); });
    started = true;
    refresh();
    setInterval(refresh, REFRESH_MS);
    setInterval(tickUpdated, 1000);
  }

  document.addEventListener("DOMContentLoaded", start);

  return {
    REFRESH_MS: REFRESH_MS,
    getJSON: getJSON,
    postJSON: postJSON,
    poll: poll,
    refresh: refresh,
    format: {
      show: show,
      num: num,
      duration: duration,
      rate: rate,
      percent: percent,
      coins: coins,
      ago: ago,
      shortAddress: shortAddress,
    },
    h: h,
    clear: clear,
    card: card,
    panel: panel,
    copyButton: copyButton,
    screen: screen,
  };
})();
