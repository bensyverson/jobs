package job

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSchema_ValidJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := RunSchema(&buf); err != nil {
		t.Fatalf("RunSchema: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("schema is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(buf.Bytes()) == 0 || buf.Bytes()[buf.Len()-1] != '\n' {
		t.Error("schema output should end with a newline")
	}
}

func TestSchema_DeclaresRequiredTitle(t *testing.T) {
	var buf bytes.Buffer
	if err := RunSchema(&buf); err != nil {
		t.Fatalf("RunSchema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(buf.Bytes(), &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing top-level properties")
	}
	tasks, ok := props["tasks"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing properties.tasks")
	}
	if tasks["type"] != "array" {
		t.Errorf("schema: properties.tasks.type = %v, want array", tasks["type"])
	}
	items, ok := tasks["items"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing properties.tasks.items")
	}
	required, ok := items["required"].([]any)
	if !ok {
		t.Fatal("schema: missing properties.tasks.items.required")
	}
	found := false
	for _, r := range required {
		if s, _ := r.(string); s == "title" {
			found = true
		}
	}
	if !found {
		t.Errorf("schema: items.required must contain \"title\", got %v", required)
	}

	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing items.properties")
	}
	blockedBy, ok := itemProps["blockedBy"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing items.properties.blockedBy")
	}
	if blockedBy["type"] != "array" {
		t.Errorf("blockedBy.type = %v, want array", blockedBy["type"])
	}
	bItems, ok := blockedBy["items"].(map[string]any)
	if !ok {
		t.Fatal("blockedBy.items missing")
	}
	if bItems["type"] != "string" {
		t.Errorf("blockedBy.items.type = %v, want string", bItems["type"])
	}

	children, ok := itemProps["children"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing items.properties.children")
	}
	if children["type"] != "array" {
		t.Errorf("children.type = %v, want array", children["type"])
	}
}

// 9s2qL — the grammar's two root/provenance keys must be in the published
// schema: `kind` with its enum, `foundIn` as a single string.
func TestSchema_DeclaresKindAndFoundIn(t *testing.T) {
	var buf bytes.Buffer
	if err := RunSchema(&buf); err != nil {
		t.Fatalf("RunSchema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(buf.Bytes(), &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props := schema["properties"].(map[string]any)
	tasks := props["tasks"].(map[string]any)
	items := tasks["items"].(map[string]any)
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing items.properties")
	}

	kind, ok := itemProps["kind"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing items.properties.kind")
	}
	if kind["type"] != "string" {
		t.Errorf("kind.type = %v, want string", kind["type"])
	}
	enum, ok := kind["enum"].([]any)
	if !ok {
		t.Fatal("schema: kind must declare an enum")
	}
	want := map[string]bool{"task": true, "issue": true}
	if len(enum) != len(want) {
		t.Fatalf("kind.enum = %v, want task and issue", enum)
	}
	for _, e := range enum {
		s, _ := e.(string)
		if !want[s] {
			t.Errorf("kind.enum has unexpected value %q", s)
		}
	}
	if desc, _ := kind["description"].(string); desc == "" {
		t.Error("kind needs a description")
	}

	foundIn, ok := itemProps["foundIn"].(map[string]any)
	if !ok {
		t.Fatal("schema: missing items.properties.foundIn")
	}
	if foundIn["type"] != "string" {
		t.Errorf("foundIn.type = %v, want string (one source per task)", foundIn["type"])
	}
	if desc, _ := foundIn["description"].(string); desc == "" {
		t.Error("foundIn needs a description")
	}
}
