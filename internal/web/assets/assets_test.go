package assets_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/web/assets"
)

// knownAsset is a file we know the scaffold ships; swap it if the
// underlying filename changes.
const knownAsset = "css/components.css"

func TestBuildManifest_CoversKnownAsset(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	url := m.URL(knownAsset)
	if url == "" {
		t.Fatalf("Manifest.URL(%q): empty — asset not in manifest", knownAsset)
	}
}

func TestManifestURL_ContainsFingerprint(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	url := m.URL(knownAsset)
	// Shape: /static/css/components.<hex>.css — hex segment before ext.
	re := regexp.MustCompile(`^/static/css/components\.[0-9a-f]{6,}\.css$`)
	if !re.MatchString(url) {
		t.Errorf("URL(%q) = %q, want /static/css/components.<hex>.css", knownAsset, url)
	}
}

func TestManifestURL_DeterministicAcrossBuilds(t *testing.T) {
	m1, err := assets.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := assets.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m1.URL(knownAsset) != m2.URL(knownAsset) {
		t.Errorf("fingerprint drift between builds: %q vs %q", m1.URL(knownAsset), m2.URL(knownAsset))
	}
}

func TestManifestURL_UnknownAssetReturnsEmpty(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	if got := m.URL("does/not/exist.css"); got != "" {
		t.Errorf("URL(unknown) = %q, want empty string", got)
	}
}

func TestHandler_ServesHashedURL_WithImmutableCache(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.StripPrefix("/static/", m.Handler()))
	defer ts.Close()

	resp, err := http.Get(ts.URL + m.URL(knownAsset))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: status %d, want 200", m.URL(knownAsset), resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Errorf("GET %s: empty body", m.URL(knownAsset))
	}
	cc := resp.Header.Get("Cache-Control")
	// Long-lived cache with the immutable directive, per common fingerprint-cache practice.
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=") {
		t.Errorf("Cache-Control = %q, want immutable + max-age=<long>", cc)
	}
}

func TestHandler_UnhashedPath_NotFound(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.StripPrefix("/static/", m.Handler()))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/static/" + knownAsset)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET unhashed /static/%s: status %d, want 404 (only fingerprinted URLs are served)", knownAsset, resp.StatusCode)
	}
}

// TestComponentsCSS_StripCollapsesByDefault pins the slide-up
// transition contract: the strip is rendered into the DOM at all
// times (so accessibility tools can announce it on toggle), but its
// max-height is 0 in live mode and grows when the page enters
// scrubbing mode. Without max-height: 0, the strip sits visible at
// full height and the slide-up animation has nothing to interpolate.
func TestComponentsCSS_StripCollapsesByDefault(t *testing.T) {
	body, err := fs.ReadFile(assets.FS(), "css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	baseRule := regexp.MustCompile(`(?s)\.c-scrubber-strip\s*{[^}]*}`).Find(body)
	if baseRule == nil {
		t.Fatal("missing .c-scrubber-strip rule")
	}
	if !regexp.MustCompile(`max-height\s*:\s*0`).Match(baseRule) {
		t.Errorf(".c-scrubber-strip must set max-height: 0 in the base rule for the slide-up transition")
	}
	if !regexp.MustCompile(`overflow\s*:\s*hidden`).Match(baseRule) {
		t.Errorf(".c-scrubber-strip must set overflow: hidden so collapsed content is clipped")
	}
	if !regexp.MustCompile(`transition\s*:`).Match(baseRule) {
		t.Errorf(".c-scrubber-strip must declare a transition for the slide-up animation")
	}
	scrubbingRule := regexp.MustCompile(`(?s)\.page--scrubbing\s+\.c-scrubber-strip\s*{[^}]*}`).Find(body)
	if scrubbingRule == nil {
		t.Fatal("missing .page--scrubbing .c-scrubber-strip rule")
	}
	if !regexp.MustCompile(`max-height\s*:\s*[^0]`).Match(scrubbingRule) {
		t.Errorf(".page--scrubbing .c-scrubber-strip must set a non-zero max-height to expand the strip")
	}
}

// TestComponentsCSS_PillIconsTogglePerMode pins the pill's two icons:
// the back-arrow shows in live mode (page is NOT .page--scrubbing),
// the dot shows in scrubbing mode. CSS toggles via the parent class
// so JS only owns the label text + aria attributes.
func TestComponentsCSS_PillIconsTogglePerMode(t *testing.T) {
	body, err := fs.ReadFile(assets.FS(), "css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	src := string(body)
	// In live mode the dot is hidden (lives in the scrubbing-only branch).
	if !regexp.MustCompile(`(?m)^\s*\.page:not\(\.page--scrubbing\)\s+\.c-scrubber-pill__dot\s*{[^}]*display\s*:\s*none`).MatchString(src) {
		t.Errorf("expected `.page:not(.page--scrubbing) .c-scrubber-pill__dot { display: none }` to hide the live-state dot in live mode")
	}
	// In scrubbing mode the back-arrow is hidden.
	if !regexp.MustCompile(`(?m)^\s*\.page--scrubbing\s+\.c-scrubber-pill__icon\s*{[^}]*display\s*:\s*none`).MatchString(src) {
		t.Errorf("expected `.page--scrubbing .c-scrubber-pill__icon { display: none }` to hide the back-arrow when scrubbing")
	}
}

// TestComponentsCSS_CursorDotAtTop pins the cursor grip's vertical
// anchor. The dot sits at the top of the time cursor, matching the
// design — earlier iterations placed it at the bottom.
func TestComponentsCSS_CursorDotAtTop(t *testing.T) {
	body, err := fs.ReadFile(assets.FS(), "css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	rule := regexp.MustCompile(`(?s)\.c-scrubber-strip__cursor::after\s*{[^}]*}`)
	match := rule.Find(body)
	if match == nil {
		t.Fatal("components.css: missing .c-scrubber-strip__cursor::after rule")
	}
	if regexp.MustCompile(`\bbottom\s*:`).Match(match) {
		t.Errorf(".c-scrubber-strip__cursor::after still anchors via `bottom:`; should anchor via `top:` so the grip sits at the top of the cursor")
	}
	if !regexp.MustCompile(`\btop\s*:`).Match(match) {
		t.Errorf(".c-scrubber-strip__cursor::after missing `top:` anchor")
	}
}

// TestComponentsCSS_PlanRowDescPreservesLinebreaks pins the plan row's
// description treatment: newlines in the task description must render
// as linebreaks, matching the peek sheet and full task page (.c-note
// uses white-space: pre-wrap). Both render paths — the server template
// (plan.html.tmpl) and the client scrub renderer (plan-scrub-render.mjs)
// — share the .c-plan-row__desc class, so the CSS rule is the single
// place that controls this.
func TestComponentsCSS_PlanRowDescPreservesLinebreaks(t *testing.T) {
	body, err := fs.ReadFile(assets.FS(), "css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	rule := regexp.MustCompile(`(?s)\.c-plan-row__desc\s*{[^}]*}`).Find(body)
	if rule == nil {
		t.Fatal("components.css: missing .c-plan-row__desc rule")
	}
	if !regexp.MustCompile(`white-space\s*:\s*pre-wrap`).Match(rule) {
		t.Errorf(".c-plan-row__desc must set white-space: pre-wrap so description linebreaks render, matching the peek sheet and task page")
	}
}

// TestBaseCSS_HiddenAttributeRule pins the global rule that makes the
// HTML `hidden` attribute work uniformly across components. Without
// it, any component with a class-level `display: ...` (e.g.
// `.c-history-banner { display: flex }`) overrides the UA stylesheet's
// `[hidden] { display: none }` because class beats type-only
// specificity, and the element stays visible despite `hidden`.
func TestBaseCSS_HiddenAttributeRule(t *testing.T) {
	body, err := fs.ReadFile(assets.FS(), "css/base.css")
	if err != nil {
		t.Fatalf("read base.css: %v", err)
	}
	re := regexp.MustCompile(`\[hidden\]\s*{\s*display:\s*none\s*!important\s*;?\s*}`)
	if !re.Match(body) {
		t.Error("base.css missing `[hidden] { display: none !important; }` rule — components with class-level `display:` will override the UA `hidden` attribute")
	}
}

func TestHandler_WrongHash_NotFound(t *testing.T) {
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.StripPrefix("/static/", m.Handler()))
	defer ts.Close()

	bogus := "/static/css/components.deadbeef.css"
	resp, err := http.Get(ts.URL + bogus)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s: status %d, want 404 for stale/wrong fingerprint", bogus, resp.StatusCode)
	}
}
