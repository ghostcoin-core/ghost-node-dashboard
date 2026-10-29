package server

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The Rewards screen links its own stylesheet, which must be served behind
// the key like every other static file and keep to the page's CSP.
func TestRewardsScreenStylesheet(t *testing.T) {
	h := newHandler(t, nil)
	js := getBody(t, h, "/static/screens/rewards.js")
	if !strings.Contains(js, `"/static/screens/rewards.css"`) {
		t.Fatal("rewards.js does not link /static/screens/rewards.css")
	}
	css := getBody(t, h, "/static/screens/rewards.css")
	if rec := do(h, http.MethodGet, "/static/screens/rewards.css", goodHost, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("rewards.css without cookie: status %d, want 401", rec.Code)
	}
	if regexp.MustCompile(`(?i)https?:|url\(`).MatchString(css) {
		t.Error("rewards.css loads something from elsewhere")
	}

	// Every rule is scoped to the screen root, so it cannot touch the others.
	noComments := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	noMedia := regexp.MustCompile(`@media[^{]*\{`).ReplaceAllString(noComments, "")
	for _, m := range regexp.MustCompile(`([^{}]+)\{`).FindAllStringSubmatch(noMedia, -1) {
		for _, sel := range strings.Split(m[1], ",") {
			if sel = strings.TrimSpace(sel); !strings.HasPrefix(sel, "#screen-rewards") {
				t.Errorf("rewards.css selector %q is not scoped to #screen-rewards", sel)
			}
		}
	}
	for _, want := range []string{"#B8A8FF", "min-height: 44px", "@media (max-width: 640px)"} {
		if !strings.Contains(css, want) {
			t.Errorf("rewards.css lacks %q", want)
		}
	}

	// Newer and Older are 44 px tall at every width, not only on phones.
	desktop := noComments
	if i := strings.Index(desktop, "@media"); i >= 0 {
		desktop = desktop[:i]
	}
	page := regexp.MustCompile(`#screen-rewards \.rw-page \{([^}]*)\}`).FindStringSubmatch(desktop)
	if page == nil || !strings.Contains(page[1], "min-height: 44px") {
		t.Error("rewards.css: the paging buttons are not 44 px tall outside the phone rules")
	}

	// The long chart views scroll inside their card rather than overflow it.
	if !regexp.MustCompile(`#screen-rewards \.rw-plot-scroll \{[^}]*overflow-x: auto`).MatchString(desktop) {
		t.Error("rewards.css: the chart has no sideways scroll box")
	}
}

// The gap between bars follows the width per day, so a long history keeps
// usable columns instead of fixed gaps that squeeze the bars to nothing.
func TestRewardsChartScalesGap(t *testing.T) {
	h := newHandler(t, nil)
	js := getBody(t, h, "/static/screens/rewards.js")
	for _, want := range []string{"style.columnGap", "style.minWidth", "MIN_COLUMN", `class: "rw-plot-scroll"`} {
		if !strings.Contains(js, want) {
			t.Errorf("rewards.js lacks %q", want)
		}
	}
}

// The screen sets no style attribute (the CSP refuses it), and every id it
// makes carries its own prefix.
func TestRewardsScreenMarkup(t *testing.T) {
	h := newHandler(t, nil)
	js := getBody(t, h, "/static/screens/rewards.js")
	for _, bad := range []string{`"style"`, "style="} {
		if strings.Contains(js, bad) {
			t.Errorf("rewards.js uses %q", bad)
		}
	}
	ids := regexp.MustCompile(`(?:id: |copyButton\()"([^"]+)"`).FindAllStringSubmatch(js, -1)
	if len(ids) == 0 {
		t.Fatal("rewards.js makes no element ids")
	}
	for _, m := range ids {
		if !strings.HasPrefix(m[1], "rw-") {
			t.Errorf("rewards.js id %q lacks the rw- prefix", m[1])
		}
	}
	for _, want := range []string{`"/api/rewards"`, `"/api/mining"`, `"before="`, `"limit="`, "aria-pressed", `role: "img"`} {
		if !strings.Contains(js, want) {
			t.Errorf("rewards.js lacks %q", want)
		}
	}
}
