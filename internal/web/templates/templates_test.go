package templates_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/render"
	"github.com/bensyverson/jobs/internal/web/templates"
)

func newEngine(t *testing.T) *templates.Engine {
	t.Helper()
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	e, err := templates.New(m)
	if err != nil {
		t.Fatalf("templates.New: %v", err)
	}
	return e
}

func TestLayoutMountsPeekSheetAndScript(t *testing.T) {
	e := newEngine(t)
	var buf bytes.Buffer
	if err := e.Render(&buf, "home", &homeTemplateData{}); err != nil {
		t.Fatalf("Render home: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`<peek-sheet`,
		`/static/js/peek-sheet`,
		`/static/js/peek-click`,
		`/static/js/peek-bell`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout missing %q", want)
		}
	}
}

func TestLayout_HasSkipLinkAndMainTarget(t *testing.T) {
	e := newEngine(t)
	var buf bytes.Buffer
	if err := e.Render(&buf, "home", &homeTemplateData{Chrome: templates.Chrome{ActiveTab: "home"}}); err != nil {
		t.Fatalf("Render home: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `<a href="#main" class="c-skip-link">Skip to main content</a>`) {
		t.Errorf("layout missing skip link\n---\n%s", body)
	}
	if !strings.Contains(body, `id="main"`) || !strings.Contains(body, `tabindex="-1"`) {
		t.Errorf("main is missing id/tabindex for skip-link target")
	}
}

func TestHeader_ActiveTabCarriesAriaCurrent(t *testing.T) {
	e := newEngine(t)
	for _, tab := range []string{"home", "plan", "actors", "log"} {
		var buf bytes.Buffer
		if err := e.Render(&buf, "home", &homeTemplateData{Chrome: templates.Chrome{ActiveTab: tab}}); err != nil {
			t.Fatalf("Render home (%s): %v", tab, err)
		}
		body := buf.String()
		// Each render must contain exactly one aria-current="page".
		got := strings.Count(body, `aria-current="page"`)
		if got != 1 {
			t.Errorf("ActiveTab=%q: aria-current=\"page\" count = %d, want 1\n---\n%s", tab, got, body)
		}
	}
}

func TestFooter_ConnectionStatusIsAriaLivePolite(t *testing.T) {
	e := newEngine(t)
	var buf bytes.Buffer
	if err := e.Render(&buf, "home", &homeTemplateData{Chrome: templates.Chrome{ActiveTab: "home"}}); err != nil {
		t.Fatalf("Render home: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `data-connection role="status" aria-live="polite"`) {
		t.Errorf("connection indicator missing role=status / aria-live=polite\n---\n%s", body)
	}
}

// homeTemplateData mirrors the shape of handlers.HomePageData just
// closely enough for the home template to render without missing-field
// errors. Duplicated here rather than imported from handlers because
// handlers already depends on templates; reversing that would cycle.
type homeTemplateData struct {
	templates.Chrome
	Activity          homeActivity
	NewlyBlocked      homeNewlyBlocked
	LongestClaim      homeLongestClaim
	OldestTodo        homeOldestTodo
	ActiveClaims      homeActiveClaims
	RecentCompletions homeRecentCompletions
	Upcoming          homeUpcoming
	Blocked           homeBlocked
	Graph             render.SubwayView
}

type homeUpcoming struct {
	Count int
	Rows  []homeUpcomingRow
}

type homeUpcomingRow struct {
	TaskShortID, TaskURL, TaskTitle, AgeText string
	CreatedAtUnix                            int64
}

type homeBlocked struct {
	Count int
	Rows  []homeBlockedRow
}

type homeBlockedRow struct {
	TaskShortID, TaskURL, TaskTitle string
	Blockers                        []homeBlockerLink
}

type homeBlockerLink struct {
	ShortID, URL string
}

type homeRecentCompletions struct {
	Count int
	Rows  []homeRecentCompletionRow
}

type homeRecentCompletionRow struct {
	Actor, ActorURL, TaskShortID, TaskURL, TaskTitle, AgeText string
	CompletedAtUnix                                           int64
}

type homeActiveClaims struct {
	Count int
	Rows  []homeActiveClaimRow
}

type homeActiveClaimRow struct {
	Actor, ActorURL, TaskShortID, TaskURL, TaskTitle, DurationText string
	ClaimedAtUnix                                                  int64
}

type homeActivity struct {
	Bars                                                        []homeBar
	TotalDone, TotalClaim, TotalCreate, TotalBlock, TotalEvents int
}

type homeBar struct {
	Empty                      bool
	HeightPercent              int
	Done, Claim, Create, Block int
}

type homeNewlyBlocked struct {
	Count       int
	ProgressPct int
	Items       []homeBlockRef
}

type homeBlockRef struct {
	BlockedShortID, BlockedURL, WaitingOnShortID, WaitingOnURL string
}

type homeLongestClaim struct {
	Present                                          bool
	Actor, ActorURL, TaskShortID, TaskURL, TaskTitle string
	DurationText                                     string
	ProgressPct                                      int
}

type homeOldestTodo struct {
	Present                              bool
	TaskShortID, TaskURL, Title, AgeText string
	ProgressPct                          int
}

func renderHome(t *testing.T, e *templates.Engine) string {
	t.Helper()
	data := homeTemplateData{Chrome: templates.Chrome{ActiveTab: "home"}}
	var buf bytes.Buffer
	if err := e.Render(&buf, "home", data); err != nil {
		t.Fatalf("Render home: %v", err)
	}
	return buf.String()
}

func TestEngine_RenderHome_EmitsHTMLSkeleton(t *testing.T) {
	out := renderHome(t, newEngine(t))
	mustContain(t, out, "<!doctype html>")
	mustContain(t, out, "<html")
	mustContain(t, out, "</html>")
	mustContain(t, out, "<main")
	mustContain(t, out, "</main>")
	mustContain(t, out, `<div class="page">`)
}

func TestEngine_Render_IncludesHeaderAndFooterPartials(t *testing.T) {
	out := renderHome(t, newEngine(t))
	mustContain(t, out, `class="c-header"`)
	mustContain(t, out, `class="c-footer"`)
	mustContain(t, out, `class="c-tabs"`)
}

func TestEngine_Render_HeaderHasAllFourTabs(t *testing.T) {
	out := renderHome(t, newEngine(t))
	// Tabs point at real routes (vision §6.4), not the prototype's
	// filesystem hrefs.
	for _, href := range []string{"/", "/plan", "/actors", "/log"} {
		needle := `href="` + href + `"`
		if !strings.Contains(out, needle) {
			t.Errorf("header missing tab link %q\n---\n%s", needle, out)
		}
	}
}

func TestEngine_Render_ActiveTabReceivesActiveClass(t *testing.T) {
	out := renderHome(t, newEngine(t))
	// With ActiveTab="home", the Home tab gets c-tab--active.
	if !strings.Contains(out, `href="/" class="c-tab c-tab--active"`) {
		t.Errorf("home tab missing c-tab--active modifier\n---\n%s", out)
	}
	// Other tabs must not be marked active.
	if strings.Contains(out, `href="/plan" class="c-tab c-tab--active"`) {
		t.Errorf("plan tab unexpectedly active on home page")
	}
}

func TestEngine_Render_FooterHasMetricsAndScrubberPill(t *testing.T) {
	out := renderHome(t, newEngine(t))
	mustContain(t, out, `class="c-footer__metric"`)
	mustContain(t, out, `class="c-scrubber-pill"`)
	mustContain(t, out, `class="c-footer__heartbeat"`)
}

// The footer metric strip prefixes the four metrics with a "Now —"
// label. The strip is always-present (always-current), so the label
// renders unconditionally rather than only when the scrubber is open.
func TestEngine_Render_FooterMetricsRenderValuesWithNowLabel(t *testing.T) {
	e := newEngine(t)
	data := homeTemplateData{Chrome: templates.Chrome{
		ActiveTab: "home",
		Footer: templates.FooterMetrics{
			Actors:       3,
			WIP:          14,
			EventsPerMin: 5,
			TasksPerHour: 22,
		},
	}}
	var buf bytes.Buffer
	if err := e.Render(&buf, "home", data); err != nil {
		t.Fatalf("Render home: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Now") {
		t.Errorf("footer should include the 'Now' label\n---\n%s", out)
	}
	// Each metric value renders as a number (not "—") inside its
	// data-metric slot.
	for _, want := range []struct{ slot, val string }{
		{"actors", "3"},
		{"wip", "14"},
		{"epm", "5"},
		{"throughput", "22"},
	} {
		needle := `data-metric="` + want.slot + `">` + want.val + `<`
		if !strings.Contains(out, needle) {
			t.Errorf("footer metric %q should render value %q; needle %q not found",
				want.slot, want.val, needle)
		}
	}
	// And the placeholder em-dash is gone from the metric slots.
	for _, slot := range []string{"actors", "wip", "epm", "throughput"} {
		emDashHole := `data-metric="` + slot + `">—<`
		if strings.Contains(out, emDashHole) {
			t.Errorf("footer metric %q still renders the em-dash placeholder", slot)
		}
	}
}

// The scrubber chrome ships rendered in the page but visually hidden
// by default. The strip stays in the DOM so the slide-up CSS
// transition has something to animate; aria-hidden + inert keep AT
// users from seeing it until the user enters scrubbing mode. The
// history banner uses the plain `hidden` attribute (no transition).
func TestEngine_Render_ScrubberStripAndHistoryBannerPresentHidden(t *testing.T) {
	out := renderHome(t, newEngine(t))
	if !strings.Contains(out, `data-scrubber-strip`) {
		t.Errorf("missing data-scrubber-strip section\n---\n%s", out)
	}
	if !strings.Contains(out, `data-scrubber-strip aria-label="Scrubber" aria-hidden="true" inert`) {
		t.Errorf("scrubber strip should be aria-hidden + inert by default (not the `hidden` attribute, which would block the transition)\n---\n%s", out)
	}
	if strings.Contains(out, `data-scrubber-strip aria-label="Scrubber" hidden`) {
		t.Errorf("scrubber strip should NOT use the `hidden` attribute — that disables the slide-up transition")
	}
	if !strings.Contains(out, `data-history-banner`) {
		t.Errorf("missing data-history-banner")
	}
	if !strings.Contains(out, `data-history-banner hidden`) {
		t.Errorf("history banner should be hidden by default")
	}
	// The cursor placeholder is in DOM so JS can position it without
	// creating elements.
	mustContain(t, out, `class="c-scrubber-strip__cursor"`)
	// The strip's in-strip "Return to live" button is removed — the
	// pill itself carries that state. Only the banner's return remains.
	if got := strings.Count(out, `data-scrubber-return`); got != 1 {
		t.Errorf("expected exactly 1 data-scrubber-return button (banner only); got %d", got)
	}
}

// The footer pill is the toggle — it reads "Time travel" with a
// back-arrow when live, swaps to "Return to live" with a pulsing
// green dot when scrubbing. Default render is the live state.
func TestEngine_Render_ScrubberPillStartsAsTimeTravel(t *testing.T) {
	out := renderHome(t, newEngine(t))
	if !strings.Contains(out, `data-scrubber-pill-label>Time travel<`) {
		t.Errorf("pill should default to label 'Time travel'\n---\n%s", out)
	}
	if !strings.Contains(out, `data-scrubber-pill-icon-back`) {
		t.Errorf("pill should ship a back-arrow icon (data-scrubber-pill-icon-back) for the live state")
	}
	if !strings.Contains(out, `data-scrubber-pill-icon-live`) {
		t.Errorf("pill should ship a live-state dot (data-scrubber-pill-icon-live) for the scrubbing state")
	}
}

func TestEngine_Render_UsesFingerprintedCSSURL(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	hashed := m.URL("css/components.css")
	if hashed == "" {
		t.Fatal("manifest missing components.css entry")
	}
	e, err := templates.New(m)
	if err != nil {
		t.Fatalf("templates.New: %v", err)
	}
	var buf bytes.Buffer
	data := homeTemplateData{Chrome: templates.Chrome{ActiveTab: "home"}}
	if err := e.Render(&buf, "home", data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, hashed) {
		t.Errorf("rendered output missing fingerprinted components.css URL %q", hashed)
	}
	// Fingerprint discipline: an unhashed stylesheet link would bypass
	// the immutable cache.
	if strings.Contains(out, `href="/static/css/components.css"`) {
		t.Errorf("rendered output contains unhashed CSS link — must go through the manifest")
	}
}

func TestEngine_Render_MountsLiveRegionAndScript(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	liveURL := m.URL("js/live.js")
	if liveURL == "" {
		t.Fatal("manifest missing js/live.js entry")
	}
	out := renderHome(t, newEngine(t))

	// Element is present so the live-region WebComponent attaches
	// once live.js registers it.
	if !strings.Contains(out, `<live-region src="/events">`) {
		t.Errorf("layout missing <live-region>\n---\n%s", out)
	}
	// Script is loaded with a fingerprinted URL (immutable cache).
	if !strings.Contains(out, liveURL) {
		t.Errorf("layout missing fingerprinted live.js script tag (%s)", liveURL)
	}
}

func TestEngine_Render_HeaderShipsSearchInputAndDropdown(t *testing.T) {
	out := renderHome(t, newEngine(t))
	mustContain(t, out, `data-search-root`)
	mustContain(t, out, `data-search-input`)
	mustContain(t, out, `data-search-results`)
}

func TestEngine_Render_LayoutLoadsSearchModule(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	searchURL := m.URL("js/search.mjs")
	if searchURL == "" {
		t.Fatal("manifest missing js/search.mjs entry")
	}
	out := renderHome(t, newEngine(t))
	if !strings.Contains(out, searchURL) {
		t.Errorf("layout missing fingerprinted search.mjs (%s)\n---\n%s", searchURL, out)
	}
}

func TestEngine_Render_LayoutLoadsShortcutsModule(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	shortcutsURL := m.URL("js/shortcuts.mjs")
	if shortcutsURL == "" {
		t.Fatal("manifest missing js/shortcuts.mjs entry")
	}
	out := renderHome(t, newEngine(t))
	if !strings.Contains(out, shortcutsURL) {
		t.Errorf("layout missing fingerprinted shortcuts.mjs (%s)\n---\n%s", shortcutsURL, out)
	}
}

func TestEngine_Render_LayoutLoadsFaviconModule(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	faviconURL := m.URL("js/favicon.mjs")
	if faviconURL == "" {
		t.Fatal("manifest missing js/favicon.mjs entry")
	}
	out := renderHome(t, newEngine(t))
	if !strings.Contains(out, faviconURL) {
		t.Errorf("layout missing fingerprinted favicon.mjs (%s)\n---\n%s", faviconURL, out)
	}
}

func TestEngine_Render_LayoutShipsIdleFaviconLink(t *testing.T) {
	out := renderHome(t, newEngine(t))
	mustContain(t, out, `<link rel="icon" type="image/svg+xml"`)
	// idle disc carries the muted token color, percent-encoded.
	mustContain(t, out, `%235a6967`)
}

func TestEngine_Render_HeaderTabsAdvertiseShortcuts(t *testing.T) {
	// The Issues tab sits after Plan, so it takes key 3 and Actors /
	// Log shift to 4 / 5. The hints follow header order because
	// shortcuts.mjs maps the number keys positionally.
	out := renderHome(t, newEngine(t))
	mustContain(t, out, `title="Home (press 1)"`)
	mustContain(t, out, `title="Plan (press 2)"`)
	mustContain(t, out, `title="Issues (press 3)"`)
	mustContain(t, out, `title="Actors (press 4)"`)
	mustContain(t, out, `title="Log (press 5)"`)
}

func TestEngine_Render_UnknownPageErrors(t *testing.T) {
	e := newEngine(t)
	err := e.Render(&bytes.Buffer{}, "no-such-page", templates.Chrome{})
	if err == nil {
		t.Error("Render(unknown): got nil error, want an error naming the page")
	}
}

func mustContain(t *testing.T, out, needle string) {
	t.Helper()
	if !strings.Contains(out, needle) {
		t.Errorf("rendered output missing %q\n---\n%s", needle, out)
	}
}
