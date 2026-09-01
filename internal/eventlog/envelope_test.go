package eventlog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalRoundTripsEveryField(t *testing.T) {
	want := Envelope{
		V:     1,
		Rep:   "k7Qx2m",
		Seq:   412,
		TS:    1756742400123,
		Actor: "ben",
		Type:  Type("done"),
		Task:  "VBF5u",
		Data:  json.RawMessage(`{"note":"shipped","criteria":{"aB3":"passed"}}`),
	}

	line, err := Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Unmarshal(line)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.V != want.V || got.Rep != want.Rep || got.Seq != want.Seq || got.TS != want.TS {
		t.Errorf("header round-trip: got %+v want %+v", got, want)
	}
	if got.Actor != want.Actor || got.Type != want.Type || got.Task != want.Task {
		t.Errorf("identity round-trip: got %+v want %+v", got, want)
	}
	var gotData, wantData any
	if err := json.Unmarshal(got.Data, &gotData); err != nil {
		t.Fatalf("data is not JSON: %v", err)
	}
	if err := json.Unmarshal(want.Data, &wantData); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(gotData, wantData) {
		t.Errorf("data round-trip: got %s want %s", got.Data, want.Data)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestMarshalIsExactlyOneLine(t *testing.T) {
	line, err := Marshal(Envelope{
		V: 1, Rep: "k7Qx2m", Seq: 1, TS: 2, Actor: "a", Type: "noted", Task: "t",
		Data: json.RawMessage("{\n  \"note\": \"multi\\nline\"\n}"),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if n := bytes.Count(line, []byte("\n")); n != 1 {
		t.Fatalf("want exactly 1 newline, got %d in %q", n, line)
	}
	if !bytes.HasSuffix(line, []byte("\n")) {
		t.Fatalf("want trailing newline, got %q", line)
	}
	body := line[:len(line)-1]
	if strings.TrimRight(string(body), " \t\r") != string(body) {
		t.Fatalf("trailing whitespace before newline: %q", line)
	}
}

func TestMarshalWritesTheFixedKeys(t *testing.T) {
	line, err := Marshal(Envelope{V: 1, Rep: "k7Qx2m", Seq: 1, TS: 2, Type: "created"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("not an object: %v", err)
	}
	want := []string{"v", "rep", "seq", "ts", "actor", "type", "task", "data"}
	if len(m) != len(want) {
		t.Fatalf("got keys %v, want exactly %v", keysOf(m), want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in %s", k, line)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestUnmarshalRejectsBadLines(t *testing.T) {
	cases := map[string]string{
		"unknown field":  `{"v":1,"rep":"k7Qx2m","seq":1,"ts":2,"actor":"a","type":"noted","task":"t","data":null,"extra":1}`,
		"missing rep":    `{"v":1,"seq":1,"ts":2,"actor":"a","type":"noted","task":"t","data":null}`,
		"empty rep":      `{"v":1,"rep":"","seq":1,"ts":2,"actor":"a","type":"noted","task":"t","data":null}`,
		"missing seq":    `{"v":1,"rep":"k7Qx2m","ts":2,"actor":"a","type":"noted","task":"t","data":null}`,
		"zero seq":       `{"v":1,"rep":"k7Qx2m","seq":0,"ts":2,"actor":"a","type":"noted","task":"t","data":null}`,
		"missing ts":     `{"v":1,"rep":"k7Qx2m","seq":1,"actor":"a","type":"noted","task":"t","data":null}`,
		"zero ts":        `{"v":1,"rep":"k7Qx2m","seq":1,"ts":0,"actor":"a","type":"noted","task":"t","data":null}`,
		"missing type":   `{"v":1,"rep":"k7Qx2m","seq":1,"ts":2,"actor":"a","task":"t","data":null}`,
		"wrong version":  `{"v":2,"rep":"k7Qx2m","seq":1,"ts":2,"actor":"a","type":"noted","task":"t","data":null}`,
		"not an object":  `[1,2,3]`,
		"trailing junk":  `{"v":1,"rep":"k7Qx2m","seq":1,"ts":2,"actor":"a","type":"noted","task":"t","data":null} {}`,
		"invalid json":   `{"v":1,`,
		"empty line":     ``,
		"bad replica id": `{"v":1,"rep":"has/slash","seq":1,"ts":2,"actor":"a","type":"noted","task":"t","data":null}`,
	}
	for name, line := range cases {
		if _, err := Unmarshal([]byte(line)); err == nil {
			t.Errorf("%s: want error, got none", name)
		}
	}
}

func TestUnmarshalAcceptsAnEmptyTaskAndNullData(t *testing.T) {
	e, err := Unmarshal([]byte(`{"v":1,"rep":"k7Qx2m","seq":1,"ts":2,"actor":"a","type":"purged","task":"","data":null}`))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if e.Task != "" {
		t.Errorf("Task = %q, want empty", e.Task)
	}
	if e.Data != nil {
		t.Errorf("Data = %s, want nil", e.Data)
	}
}

func TestUnmarshalToleratesATrailingNewline(t *testing.T) {
	line, err := Marshal(Envelope{V: 1, Rep: "k7Qx2m", Seq: 1, TS: 2, Type: "noted"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unmarshal(line); err != nil {
		t.Fatalf("Unmarshal of a marshalled line: %v", err)
	}
}
