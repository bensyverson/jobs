package handlers_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/job"
)

// TestProseParity_GoAndJSAgree renders the shared fixtures plus a set of
// awkward inputs through both internal/job/prose.go and assets/js/prose.mjs
// and fails on the first divergence. The fixtures pin each renderer to the
// contract; this test pins them to each other on inputs nobody wrote a
// fixture for.
func TestProseParity_GoAndJSAgree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping cross-language prose parity")
	}

	raw, err := os.ReadFile("../../job/testdata/prose_cases.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var cases []struct {
		Name  string         `json:"name"`
		Input string         `json:"input"`
		Links job.ProseLinks `json:"links"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	inputs := make([]string, 0, len(cases)+24)
	links := make([]job.ProseLinks, 0, len(cases)+24)
	names := make([]string, 0, len(cases)+24)
	for _, c := range cases {
		inputs = append(inputs, c.Input)
		links = append(links, c.Links)
		names = append(names, c.Name)
	}
	// The extras carry one shared resolver so the inline pass is exercised
	// on inputs nobody wrote a fixture for. "toString" is deliberately
	// absent from it: a naive `links[token]` lookup in JS finds
	// Object.prototype.toString and would link it.
	extraLinks := job.ProseLinks{
		"mk4Qz1": "/tasks/mk4Qz1",
		"Bp7":    "/tasks/mk4Qz1#crit-Bp7",
	}
	extra := map[string]string{
		"only whitespace":               "   \n\t\n",
		"marker without content":        "- \n1. \n",
		"bullet after paragraph":        "text\n- a\ntext again\n\n\n- b\n",
		"deep nesting":                  "- a\n  - b\n    - c\n      - d\n- e",
		"ordered inside unordered":      "- a\n  1. x\n  2. y\n- b",
		"mixed markers end list":        "- a\n1. b\n- c",
		"fence inside paragraph":        "text\n```\ncode\n```\nmore",
		"longer closing fence":          "```\nx\n`````\ny",
		"shorter closing fence ignored": "````\nx\n```\ny\n````",
		"tab indents":                   "-\ta\n\tb\n- c",
		"hard break at end":             "a\\\n\nb  ",
		"nine digits":                   "123456789. a\n1234567890. b",
		"unicode":                       "héllo\nwörld\n\n- ✓ done\n- ✗ not",
		"crlf inside fence":             "```\r\na\r\n\r\nb\r\n```\r\n",
		"quotes and entities":           "\"it's\" & <b>bold</b> 'single'",
		"item with blank then outdent":  "- a\n\n  b\n\nc",
		"prototype key is not a link":   "call toString and `toString` and constructor",
		"backtick soup":                 "`a``b` c ``` d `` e",
		"bracket soup":                  "[a](b) [c](/d) [e](https://f) [g](`h`) [](/i)",
		"id glued to punctuation":       "(mk4Qz1), mk4Qz1. mk4Qz1-Bp7 xmk4Qz1",
		"id inside a link target":       "[mk4Qz1](/tasks/mk4Qz1) and [`Bp7`](/x)",
		"code span holding a url":       "`https://example.com` and `[a](/b)`",
		"empty code span":               "a `` b ` c",
		"unicode next to an id":         "héllo mk4Qz1 wörld ✓Bp7",
	}
	for name, in := range extra {
		inputs = append(inputs, in)
		links = append(links, extraLinks)
		names = append(names, name)
	}

	module, err := filepath.Abs("../assets/js/prose.mjs")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	driver := filepath.Join(t.TempDir(), "driver.mjs")
	script := `import { renderProseHTML } from ` + jsonString(t, "file://"+module) + `;
const { inputs, links } = JSON.parse(process.argv[2]);
process.stdout.write(JSON.stringify(inputs.map((t, i) => renderProseHTML(t, links[i] ?? {}))));
`
	if err := os.WriteFile(driver, []byte(script), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	payload, _ := json.Marshal(struct {
		Inputs []string         `json:"inputs"`
		Links  []job.ProseLinks `json:"links"`
	}{inputs, links})
	cmd := exec.Command(node, driver, string(payload))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var js []string
	if err := json.Unmarshal(out, &js); err != nil {
		t.Fatalf("parse node output: %v\n%s", err, out)
	}
	if len(js) != len(inputs) {
		t.Fatalf("node returned %d results for %d inputs", len(js), len(inputs))
	}
	for i, in := range inputs {
		if got, want := js[i], job.RenderProseHTML(in, links[i]); got != want {
			t.Errorf("%s: renderers diverge\ninput: %q\n   go: %q\n   js: %q", names[i], in, want, got)
		}
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
