package job

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// statsSchemaFile is the checked-in copy the docs site publishes;
// `make docs-schema` regenerates it.
const statsSchemaFile = "../../docs/content/docs/machine-interface/_stats_schema.json"

func statsSchema(t *testing.T) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteSchema(&buf, SchemaStats); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatalf("stats schema is not JSON: %v\n%s", err, buf.String())
	}
	return s
}

func TestStatsSchema_DeclaresDraftAndTitle(t *testing.T) {
	s := statsSchema(t)
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
	if title, _ := s["title"].(string); !strings.Contains(title, "job stats") {
		t.Errorf("title = %q, want it to name job stats", title)
	}
}

func TestStatsSchema_PinsTheSchemaVersion(t *testing.T) {
	props := statsSchema(t)["properties"].(map[string]any)
	version := props["schema"].(map[string]any)
	if version["const"] != float64(ReportSchema) {
		t.Errorf("properties.schema.const = %v, want %d", version["const"], ReportSchema)
	}
}

// TestStatsSchema_MatchesTheReportStruct walks Report's JSON shape and
// the schema side by side, so a field added to the struct without the
// schema (or the reverse) fails here rather than at a reader.
func TestStatsSchema_MatchesTheReportStruct(t *testing.T) {
	s := statsSchema(t)
	compareSchema(t, "Report", reflect.TypeFor[Report](), s, s)
}

func compareSchema(t *testing.T, path string, typ reflect.Type, node, root map[string]any) {
	t.Helper()
	node = resolveRef(t, node, root)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		if typ.String() == "time.Time" {
			if node["format"] != "date-time" {
				t.Errorf("%s: a time must be a date-time string, got %v", path, node)
			}
			return
		}
		props, _ := node["properties"].(map[string]any)
		var fields []string
		for f := range typ.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			fields = append(fields, name)
			sub, ok := props[name].(map[string]any)
			if !ok {
				t.Errorf("%s.%s: in the struct, missing from the schema", path, name)
				continue
			}
			compareSchema(t, path+"."+name, f.Type, sub, root)
		}
		for name := range props {
			if !slices.Contains(fields, name) {
				t.Errorf("%s.%s: in the schema, missing from the struct", path, name)
			}
		}
		if node["additionalProperties"] != false {
			t.Errorf("%s: objects should close with additionalProperties: false", path)
		}
	case reflect.Slice:
		items, ok := node["items"].(map[string]any)
		if !ok {
			t.Errorf("%s: a slice needs items", path)
			return
		}
		compareSchema(t, path+"[]", typ.Elem(), items, root)
	}
}

func resolveRef(t *testing.T, node, root map[string]any) map[string]any {
	t.Helper()
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	name, found := strings.CutPrefix(ref, "#/$defs/")
	defs, _ := root["$defs"].(map[string]any)
	def, ok := defs[name].(map[string]any)
	if !found || !ok {
		t.Fatalf("unresolvable $ref %q", ref)
	}
	return def
}

func TestStatsSchema_CheckedInCopyIsCurrent(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSchema(&buf, SchemaStats); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	disk, err := os.ReadFile(statsSchemaFile)
	if err != nil {
		t.Fatalf("read checked-in schema: %v (run `make docs-schema`)", err)
	}
	if !bytes.Equal(disk, buf.Bytes()) {
		t.Errorf("%s is stale; run `make docs-schema`", statsSchemaFile)
	}
}

func TestWriteSchema_PlanIsTheImportGrammar(t *testing.T) {
	var plan, legacy bytes.Buffer
	if err := WriteSchema(&plan, SchemaPlan); err != nil {
		t.Fatal(err)
	}
	if err := RunSchema(&legacy); err != nil {
		t.Fatal(err)
	}
	if plan.String() != legacy.String() {
		t.Error("WriteSchema(SchemaPlan) should print the import grammar")
	}
}

func TestParseSchemaKind(t *testing.T) {
	for raw, want := range map[string]SchemaKind{"": SchemaPlan, "plan": SchemaPlan, "stats": SchemaStats, " Stats ": SchemaStats} {
		if got, ok := ParseSchemaKind(raw); !ok || got != want {
			t.Errorf("ParseSchemaKind(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	if _, ok := ParseSchemaKind("report"); ok {
		t.Error("ParseSchemaKind(report) should refuse")
	}
}
