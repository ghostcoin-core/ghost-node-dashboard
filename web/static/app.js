"use strict";

(function () {
  var REFRESH_MS = 5000;

  function text(id, value) {
    document.getElementById(id).textContent = value;
  }

  function show(v, format) {
    if (v === null || v === undefined) return "–";
    return format ? format(v) : String(v);
  }

  function yesNo(v) {
    return v ? "Yes" : "No";
  }

  function setState(label, kind) {
    var el = document.getElementById("state");
    el.textContent = label;
    el.className = "pill pill-" + kind;
  }

  function setMessage(msg) {
    var el = document.getElementById("message");
    el.textContent = msg || "";
    el.hidden = !msg;
  }

  function render(s) {
    if (s.running) {
      setState("Running", "ok");
      setMessage("");
    } else {
      setState("Not running", "bad");
      setMessage(s.error === "node not running"
        ? "The node is not running. Start it and this page will update by itself."
        : s.error || "The node is not running.");
    }
    text("chainId", show(s.chainId));
    text("blockNumber", show(s.blockNumber, function (n) { return n.toLocaleString(); }));
    text("peerCount", show(s.peerCount));
    text("syncing", show(s.syncing, yesNo));
    text("mining", show(s.mining, yesNo));
    text("payoutAddress", show(s.payoutAddress));
    text("clientVersion", show(s.clientVersion));
    text("updated", new Date().toLocaleTimeString());
  }

  function refresh() {
    fetch("/api/status", { credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (r.status === 401) {
          throw new Error("This browser is not signed in. Open the dashboard from the link the installer printed.");
        }
        if (!r.ok) throw new Error("The dashboard answered " + r.status + ".");
        return r.json();
      })
      .then(render)
      .catch(function (err) {
        setState("No answer", "bad");
        setMessage(err.message);
      });
  }

  refresh();
  setInterval(refresh, REFRESH_MS);
})();
