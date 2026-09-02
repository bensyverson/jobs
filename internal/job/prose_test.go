package job

import (
	"encoding/json"
	"os"
	"testing"
)

// proseCase is one row of testdata/prose_cases.json, shared with the
// dashboard's JS twin so the two renderers cannot drift.
type proseCase struct {
	Name  string `json:"name"`
	Input string `json:"input"`
	Text  string `json:"text"`
	HTML  string `json:"html"`
}

func loadProseCases(t *testing.T) []proseCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/prose_cases.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var cases []proseCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no fixture cases")
	}
	return cases
}

func TestRenderProseText_Fixtures(t *testing.T) {
	for _, c := range loadProseCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			if got := RenderProseText(c.Input); got != c.Text {
				t.Errorf("\n got: %q\nwant: %q", got, c.Text)
			}
		})
	}
}

func TestParseProse_Structure(t *testing.T) {
	blocks := ParseProse("intro\n\n- a\n  - b\n\n```go\nx\n```")
	if len(blocks) != 3 {
		t.Fatalf("want 3 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Kind != ProseParagraph || len(blocks[0].Lines) != 1 || blocks[0].Lines[0] != "intro" {
		t.Errorf("block 0: %+v", blocks[0])
	}
	list := blocks[1]
	if list.Kind != ProseList || list.Ordered || len(list.Items) != 1 {
		t.Fatalf("block 1: %+v", list)
	}
	item := list.Items[0]
	if len(item) != 2 || item[0].Kind != ProseParagraph || item[1].Kind != ProseList {
		t.Errorf("item blocks: %+v", item)
	}
	code := blocks[2]
	if code.Kind != ProseCode || code.Info != "go" || len(code.Lines) != 1 || code.Lines[0] != "x" {
		t.Errorf("block 2: %+v", code)
	}
}

func TestParseProse_OrderedStart(t *testing.T) {
	blocks := ParseProse("3. c\n4. d")
	if len(blocks) != 1 || !blocks[0].Ordered || blocks[0].Start != 3 || len(blocks[0].Items) != 2 {
		t.Fatalf("got %+v", blocks)
	}
}

func TestParseProse_HardBreakSplitsLines(t *testing.T) {
	blocks := ParseProse("a\\\nb  \nc\nd")
	if len(blocks) != 1 {
		t.Fatalf("got %+v", blocks)
	}
	want := []string{"a", "b", "c d"}
	got := blocks[0].Lines
	if len(got) != len(want) {
		t.Fatalf("lines: got %q want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestRenderProseHTML_Fixtures(t *testing.T) {
	for _, c := range loadProseCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			if got := RenderProseHTML(c.Input); got != c.HTML {
				t.Errorf("\n got: %q\nwant: %q", got, c.HTML)
			}
		})
	}
}
