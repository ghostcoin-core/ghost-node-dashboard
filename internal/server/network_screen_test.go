package server

import (
	"regexp"
	"strings"
	"testing"
)

// The Network screen keeps its looks in its own stylesheet, scoped to the
// screen's root, and loads it itself; its element ids carry the nw- prefix.
func TestNetworkScreenFiles(t *testing.T) {
	h := newHandler(t, nil)
	js := getBody(t, h, "/static/screens/network.js")
	css := getBody(t, h, "/static/screens/network.css")

	for _, want := range []string{`"/static/screens/network.css"`, `rel: "stylesheet"`, `"/api/network"`, `"/api/log"`, `"/api/rewards"`, `"/api/node"`, `"aria-pressed"`} {
		if !strings.Contains(js, want) {
			t.Errorf("network.js lacks %s", want)
		}
	}
	// Markup is built with GhostDash.h and textContent; no style attribute.
	for _, bad := range []string{"HTML", `"style"`, "style="} {
		if strings.Contains(js, bad) {
			t.Errorf("network.js uses %s", bad)
		}
	}
	for _, m := range regexp.MustCompile(`id: "([^"]+)"`).FindAllStringSubmatch(js, -1) {
		if !strings.HasPrefix(m[1], "nw-") {
			t.Errorf("network.js id %q lacks the nw- prefix", m[1])
		}
	}

	// Every selector, outside and inside @media, starts with #screen-network.
	plain := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	selectors := 0
	for _, m := range regexp.MustCompile(`([^{}]+)\{`).FindAllStringSubmatch(plain, -1) {
		sel := strings.TrimSpace(m[1])
		if strings.HasPrefix(sel, "@media") {
			continue
		}
		for _, part := range strings.Split(sel, ",") {
			selectors++
			if !strings.HasPrefix(strings.TrimSpace(part), "#screen-network") {
				t.Errorf("network.css selector %q is not scoped to #screen-network", part)
			}
		}
	}
	if selectors == 0 {
		t.Error("network.css has no rules")
	}
	if strings.Contains(css, "import") || strings.Contains(css, "url(") {
		t.Error("network.css loads another file")
	}
}
