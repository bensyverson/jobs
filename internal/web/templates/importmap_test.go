package templates_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/web/assets"
)

// Assets are content-fingerprinted, so a module imported as
// "./position.mjs" resolves against the *hashed* URL of the importer and
// lands on /static/js/position.mjs — which the manifest handler 404s on
// purpose. The layout's importmap is what maps the unhashed specifier onto
// the fingerprinted URL, and a shared module missing from it fails the whole
// module graph *silently*: no console error, the page just quietly loses
// every feature downstream of it.
//
// This test is the guard: every relative import in assets/js/*.mjs must have
// an importmap entry.

var relativeImportRe = regexp.MustCompile(`from\s+"\./([A-Za-z0-9._-]+\.mjs)"`)

func TestLayoutImportmapCoversEveryRelativeModuleImport(t *testing.T) {
	dir := filepath.Join("..", "assets", "js")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	layout := renderHome(t, newEngine(t))

	needed := map[string][]string{} // imported module -> importers
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mjs") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, match := range relativeImportRe.FindAllStringSubmatch(string(src), -1) {
			needed[match[1]] = append(needed[match[1]], e.Name())
		}
	}
	if len(needed) == 0 {
		t.Fatal("found no relative module imports; the scanner is broken")
	}

	for name, importers := range needed {
		specifier := `"/static/js/` + name + `":`
		if !strings.Contains(layout, specifier) {
			t.Errorf("js/%s is imported by %v but has no importmap entry in the layout — "+
				"the fingerprinted URL will 404 and the importers will fail silently",
				name, importers)
			continue
		}
		url := m.URL("js/" + name)
		if url == "" {
			t.Errorf("manifest has no entry for js/%s", name)
			continue
		}
		if !strings.Contains(layout, specifier+` "`+url+`"`) {
			t.Errorf("importmap entry for js/%s does not point at its fingerprinted URL %s", name, url)
		}
	}
}
