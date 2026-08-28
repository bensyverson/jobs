package handlers

// Cross-language parity for one log row.
//
// The Log view renders rows twice: server-side through the "log-row"
// block in pages/log.html.tmpl, and client-side through
// assets/js/log-row.mjs when an event arrives over SSE. A live row that
// doesn't match its SSR twin is a bug users see (task vz1tg: live
// found_in_set rows rendered FOUND_IN_SET with no payload). These tests
// render the same event both ways and diff the normalised markup, so
// the two renderers cannot drift.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// parityFixture is one event rendered through both renderers. Detail is
// the raw JSON the event store holds, exactly as it reaches the client
// on the SSE `detail` field.
type parityFixture struct {
	eventType string
	actor     string
	detail    string
}

// parityFixtures covers every type in knownEventTypes plus the event
// types that carry bespoke verb or metadata handling without appearing
// in the filter-chip strip (claim_expired, labeled) and a couple of
// plain pass-through types. TestLogRowParity_CoversKnownEventTypes
// fails if a knownEventTypes entry is missing here.
var parityFixtures = []parityFixture{
	{"created", "alice", `{"title":"Root task"}`},
	{"claimed", "alice", `{"duration":"30m"}`},
	{"done", "alice", `{"note":"shipped it"}`},
	{"done", "alice", `{"note":""}`},
	{"blocked", "bob", `{"blocker_id":"AbC12"}`},
	{"unblocked", "bob", `{"blocker_id":"AbC12"}`},
	{"noted", "agent-loglive", `{"text":"a note body with <angle> & \"quote\" chars"}`},
	{"criteria_added", "alice", `{"criteria":[{"label":"tests pass"},{"label":"docs updated"}]}`},
	{"criterion_state", "alice", `{"label":"tests pass","state":"passed"}`},
	{"criterion_state", "alice", `{"label":"tests pass","state":"failed"}`},
	{"criterion_state", "alice", `{"label":"tests pass"}`},
	{"found_in_set", "alice", `{"source_id":"QnB2g"}`},
	{"found_in_set", "alice", `{"source_id":"QnB2g","previous_source_id":"zzTop"}`},
	{"found_in_cleared", "alice", `{"source_id":"QnB2g"}`},
	{"kind_changed", "alice", `{"from":"task","to":"issue"}`},
	{"released", "alice", `{}`},
	{"canceled", "alice", `{"reason":"obsolete"}`},
	{"labeled", "alice", `{"names":["web","bug"]}`},
	{"claim_expired", "alice", `{"duration":"30m"}`},
	{"edited", "alice", `{"title":"new title"}`},
	{"moved", "alice", `{"to":"AbC12"}`},
	{"reopened", "alice", `{}`},
	{"focus_set", "alice", `{"task":"AbC12"}`},
	{"focus_released", "alice", `{}`},
	{"teleported", "alice", `{"text":"an event type the server has never heard of"}`},
}

const parityTitle = `A task title with <angle> & "quote" chars`

// logRowMJSPath is the on-disk path of the client renderer. The Go test
// drives it through node so both renderers see the same event.
const logRowMJSPath = "../assets/js/log-row.mjs"

func TestLogRowParity_CoversKnownEventTypes(t *testing.T) {
	have := map[string]bool{}
	for _, f := range parityFixtures {
		have[f.eventType] = true
	}
	for _, et := range knownEventTypes {
		if !have[et] {
			t.Errorf("knownEventTypes contains %q but parityFixtures has no fixture for it", et)
		}
	}
}

// TestLogRowParity_JSKnownTypesMatchServer reads the event-type list out
// of log-row.mjs and asserts it is the same list, in the same order, as
// knownEventTypes. Embedding the list on both sides is what lets the JS
// test walk the server's vocabulary; this test is what stops the two
// copies drifting.
func TestLogRowParity_JSKnownTypesMatchServer(t *testing.T) {
	src, err := readAsset("js/log-row.mjs")
	if err != nil {
		t.Fatalf("read log-row.mjs: %v", err)
	}
	got := parseJSKnownEventTypes(t, "log-row.mjs", src)
	if len(got) != len(knownEventTypes) {
		t.Fatalf("KNOWN_EVENT_TYPES in log-row.mjs = %v, want %v", got, knownEventTypes)
	}
	for i := range got {
		if got[i] != knownEventTypes[i] {
			t.Fatalf("KNOWN_EVENT_TYPES in log-row.mjs = %v, want %v", got, knownEventTypes)
		}
	}
}

func TestLogRowParity_ServerAndClientMarkupMatch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping cross-language log-row parity")
	}

	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	engine := parityEngine(t)

	type clientEvent struct {
		ID        int64  `json:"id"`
		TaskID    string `json:"task_id"`
		TaskTitle string `json:"task_title"`
		EventType string `json:"event_type"`
		Actor     string `json:"actor"`
		Detail    string `json:"detail"`
		CreatedAt string `json:"created_at"`
	}

	events := make([]clientEvent, 0, len(parityFixtures))
	serverRows := make([]string, 0, len(parityFixtures))
	for i, f := range parityFixtures {
		e := job.EventEntry{
			ID:        int64(i + 1),
			TaskID:    7,
			ShortID:   "AbC12",
			EventType: f.eventType,
			Actor:     f.actor,
			Detail:    f.detail,
			// Two minutes back so the relative-time ladder produces a
			// non-trivial label on both sides ("2m", not "just now").
			CreatedAt: now.Add(-2 * time.Minute).Unix(),
		}
		row := buildLogEventRow(e, parityTitle, now)
		var sb strings.Builder
		if err := engine.RenderFragment(&sb, "log", "log-row", row); err != nil {
			t.Fatalf("render log-row fragment for %s: %v", f.eventType, err)
		}
		serverRows = append(serverRows, normalizeRowHTML(sb.String()))
		events = append(events, clientEvent{
			ID:        e.ID,
			TaskID:    e.ShortID,
			TaskTitle: parityTitle,
			EventType: e.EventType,
			Actor:     e.Actor,
			Detail:    e.Detail,
			CreatedAt: time.Unix(e.CreatedAt, 0).UTC().Format(time.RFC3339),
		})
	}

	clientRows := renderRowsWithNode(t, node, events, now)
	if len(clientRows) != len(serverRows) {
		t.Fatalf("node returned %d rows, want %d", len(clientRows), len(serverRows))
	}
	for i, f := range parityFixtures {
		want := serverRows[i]
		got := normalizeRowHTML(clientRows[i])
		if got != want {
			t.Errorf("%s (%s): live row does not match SSR row\n server: %s\n client: %s",
				f.eventType, f.detail, want, got)
		}
	}
}

// renderRowsWithNode runs log-row.mjs over the given events and returns
// one HTML string per event, in order.
func renderRowsWithNode(t *testing.T, node string, events any, now time.Time) []string {
	t.Helper()
	payload, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	abs, err := filepath.Abs(logRowMJSPath)
	if err != nil {
		t.Fatalf("abs path for log-row.mjs: %v", err)
	}
	script := fmt.Sprintf(`
import { renderLogRow } from %q;
const events = JSON.parse(process.argv[2]);
const nowSec = Number(process.argv[3]);
process.stdout.write(events.map((e) => renderLogRow(e, { nowSec })).join("\n"));
`, "file://"+abs)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "render-rows.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write node driver: %v", err)
	}
	cmd := exec.Command(node, scriptPath, string(payload), fmt.Sprintf("%d", now.Unix()))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node render: %v\n%s", err, stderr.String())
	}
	return strings.Split(string(out), "\n")
}

func parityEngine(t *testing.T) *templates.Engine {
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

func readAsset(name string) (string, error) {
	b, err := os.ReadFile(filepath.Join("../assets", filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

var jsListRe = regexp.MustCompile(`(?s)const KNOWN_EVENT_TYPES\s*=\s*\[(.*?)\]`)
var jsStringRe = regexp.MustCompile(`"([^"]*)"`)

func parseJSKnownEventTypes(t *testing.T, where, src string) []string {
	t.Helper()
	m := jsListRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no KNOWN_EVENT_TYPES array literal found", where)
	}
	var out []string
	for _, s := range jsStringRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, s[1])
	}
	return out
}

// normalizeRowHTML collapses the template's pretty-printing so the two
// renderers can be compared on structure rather than indentation. Only
// whitespace that sits *between* tags is collapsed — a space inside text
// (the "cleared, was " prefix before its id pill) is meaningful and is
// preserved.
var betweenTagsRe = regexp.MustCompile(`>\s+<`)

func normalizeRowHTML(s string) string {
	return strings.TrimSpace(betweenTagsRe.ReplaceAllString(strings.TrimSpace(s), "><"))
}

// TestLiveSubscribesToEveryEmittedEventType guards the other half of
// the live path: an SSE frame carrying `event: <type>` dispatches only
// to a listener registered for that exact name, so live.js has to name
// every event type the store can emit or those events silently never
// reach any live module. (Before task vz1tg, found_in_set,
// kind_changed, criterion_state, criteria_added, claim_expired and the
// focus events were all missing from that list — the rows never
// arrived at all.)
//
// The expected vocabulary is scraped from the recordEvent call sites in
// internal/job, so adding a new event type there fails this test until
// the client subscribes to it.
func TestLiveSubscribesToEveryEmittedEventType(t *testing.T) {
	emitted := scrapeEmittedEventTypes(t)
	if len(emitted) < 15 {
		t.Fatalf("scraped only %d event types from internal/job (%v); the scraper is broken, not the client", len(emitted), emitted)
	}

	src, err := readAsset("js/live.js")
	if err != nil {
		t.Fatalf("read live.js: %v", err)
	}
	subscribed := map[string]bool{}
	for _, s := range parseJSKnownEventTypes(t, "live.js", src) {
		subscribed[s] = true
	}
	for _, et := range emitted {
		if !subscribed[et] {
			t.Errorf("internal/job emits %q but live.js never subscribes to it, so the live view never sees it", et)
		}
	}
}

// scrapeEmittedEventTypes reads every event type passed to recordEvent /
// recordOrphanEvent in internal/job, resolving the handful that are
// named by a const. Every call site keeps its event-type argument on one
// line; the count check below fails loudly if that stops being true
// rather than letting the scraper quietly under-report.
func scrapeEmittedEventTypes(t *testing.T) []string {
	t.Helper()
	dir := "../../job"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/job: %v", err)
	}
	consts := map[string]string{}
	var sources []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// Drop the two function declarations so their parameter list
		// isn't mistaken for a call site's arguments.
		src := recordDeclRe.ReplaceAllString(string(b), "")
		sources = append(sources, src)
		for _, m := range eventConstRe.FindAllStringSubmatch(src, -1) {
			consts[m[1]] = m[2]
		}
	}

	seen := map[string]bool{}
	var out []string
	var calls, resolved int
	for _, src := range sources {
		calls += strings.Count(src, "recordEvent(") + strings.Count(src, "recordOrphanEvent(")
		for _, m := range append(
			recordEventRe.FindAllStringSubmatch(src, -1),
			recordOrphanRe.FindAllStringSubmatch(src, -1)...,
		) {
			arg := m[1]
			var et string
			switch {
			case strings.HasPrefix(arg, `"`):
				et = strings.Trim(arg, `"`)
			case consts[arg] != "":
				et = consts[arg]
			case dynamicEventTypeArgs[arg] != "":
				// A runtime-chosen verb whose possible values are all
				// already covered by literal call sites.
				resolved++
				continue
			default:
				t.Fatalf("recordEvent call passes event type %q, which this scraper cannot resolve; "+
					"add it to dynamicEventTypeArgs (with the values it can take) or use a literal", arg)
			}
			resolved++
			if !seen[et] {
				seen[et] = true
				out = append(out, et)
			}
		}
	}
	if resolved != calls {
		t.Fatalf("scraped %d event-type arguments from %d recordEvent call sites; a call site the scraper can't read means this test under-reports", resolved, calls)
	}
	sort.Strings(out)
	return out
}

// dynamicEventTypeArgs names the recordEvent arguments that are chosen
// at runtime rather than written as a literal, mapped to a note on what
// they can be. Each must only ever take values that some literal call
// site already contributes, or the scraped vocabulary is incomplete.
var dynamicEventTypeArgs = map[string]string{
	"destination": `the cascade-close verb in internal/job/tasks.go: "done" or "canceled", both emitted literally elsewhere`,
}

var (
	recordDeclRe   = regexp.MustCompile(`(?m)^func record(?:Orphan)?Event\(.*$`)
	eventConstRe   = regexp.MustCompile(`(?m)^\s*(?:const\s+)?(event[A-Za-z]+)\s*=\s*"([a-z_]+)"`)
	recordEventRe  = regexp.MustCompile(`\brecordEvent\([^,()]+,[^,()]+,\s*("[a-z_]+"|[A-Za-z][A-Za-z0-9]*)\s*,`)
	recordOrphanRe = regexp.MustCompile(`\brecordOrphanEvent\([^,()]+,\s*("[a-z_]+"|[A-Za-z][A-Za-z0-9]*)\s*,`)
)
