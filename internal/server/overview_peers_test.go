package server

import (
	"regexp"
	"strings"
	"testing"
)

// funcBody returns the body of the named function in js: the text from its
// opening line up to the first line that closes it at the same indent.
func funcBody(t *testing.T, js, name string) string {
	t.Helper()
	loc := regexp.MustCompile(`\n( *)function ` + regexp.QuoteMeta(name) + `\(`).FindStringSubmatchIndex(js)
	if loc == nil {
		t.Fatalf("function %s not found", name)
	}
	rest := js[loc[0]:]
	end := strings.Index(rest, "\n"+js[loc[2]:loc[3]]+"}")
	if end < 0 {
		t.Fatalf("function %s has no end", name)
	}
	return rest[:end]
}

// With 0 peers and mining on, the node holds off mining: the Overview says
// it is waiting for other nodes, in the sentence and in a note linking to
// Network, and the sidebar shows "0 peers" as a quiet warning.
func TestOverviewZeroPeers(t *testing.T) {
	h := newHandler(t, nil)
	js := getBody(t, h, "/static/screens/overview.js")

	const waiting = "Waiting for other nodes. Mining starts once your node connects to the network."
	if !strings.Contains(js, `var WAITING_FOR_PEERS = "`+waiting+`";`) {
		t.Errorf("overview.js lacks the waiting sentence %q", waiting)
	}

	// The condition: the node answered with 0 peers, mining is not paused
	// and the node is not stopped.
	cond := funcBody(t, js, "waitingForPeers")
	for _, want := range []string{"node.peers === 0", "!mining.paused", "!stopped()"} {
		if !strings.Contains(cond, want) {
			t.Errorf("waitingForPeers lacks %q:\n%s", want, cond)
		}
	}

	// The sentence says it in place of the mining sentence, which still
	// follows for a node with peers.
	sent := funcBody(t, js, "sentence")
	wait := strings.Index(sent, "if (waitingForPeers()) return WAITING_FOR_PEERS;")
	mining := strings.Index(sent, `"Mining on "`)
	if wait < 0 || mining < 0 || wait > mining {
		t.Errorf("sentence() does not return the waiting sentence before the mining sentence:\n%s", sent)
	}

	// The note card at the top of the screen, with a link to Network.
	note := funcBody(t, js, "peersNote")
	for _, want := range []string{`id: "ov-peers-note"`, "WAITING_FOR_PEERS", `href: "#/network"`, `text: "Network"`} {
		if !strings.Contains(note, want) {
			t.Errorf("peersNote lacks %q:\n%s", want, note)
		}
	}
	if !strings.Contains(funcBody(t, js, "renderPeersNote"), `el("ov-peers-note").hidden = !waitingForPeers();`) {
		t.Error("renderPeersNote does not show the note only while waiting for peers")
	}
	if !strings.Contains(funcBody(t, js, "renderAll"), "renderPeersNote();") {
		t.Error("renderAll does not render the peers note")
	}
	if !regexp.MustCompile(`loadStylesheet\(\);\s*root\.appendChild\(peersNote\(\)\);\s*root\.appendChild\(topRow\(\)\);`).MatchString(js) {
		t.Error("the peers note is not the first thing on the Overview")
	}
	// Pause stays: its button is still built and only hidden while paused.
	if !strings.Contains(js, `el("mining-pause").hidden = paused;`) {
		t.Error("overview.js no longer keeps the Pause button")
	}

	css := getBody(t, h, "/static/screens/overview.css")
	if !strings.Contains(css, "#screen-overview .ov-peers-note {") {
		t.Error("overview.css lacks the peers note style")
	}

	// Sidebar: "0 peers" in the quiet-warning style.
	app := getBody(t, h, "/static/app.js")
	if !strings.Contains(app, `if (s.peers === 0) count.className = "peers-none";`) {
		t.Error("app.js does not mark 0 peers with the quiet-warning style")
	}
	style := getBody(t, h, "/static/style.css")
	rule := regexp.MustCompile(`(?s)\n\.peers-none \{(.*?)\}`).FindStringSubmatch(style)
	if rule == nil {
		t.Fatal("style.css lacks the .peers-none rule")
	}
	for _, want := range []string{"background: var(--violet-tint);", "color: var(--ink);"} {
		if !strings.Contains(rule[1], want) {
			t.Errorf(".peers-none lacks %q", want)
		}
	}
}
