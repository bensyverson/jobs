package job

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

//go:fix inline
func secs(s int64) *int64 { return new(s) }

// sampleReport is a realistic four-week report, built by hand so the
// renderers are tested without a store.
func sampleReport() Report {
	loc := time.UTC
	since := time.Date(2026, 8, 31, 0, 0, 0, 0, loc)
	var series []Sample
	var activity []ActivityCount
	for i := range 4 {
		start := since.AddDate(0, 0, 7*i)
		end := start.AddDate(0, 0, 7)
		scope := 400 + 240*i
		done := 300 + 250*i
		series = append(series, Sample{End: end, Scope: scope, Done: done, Open: scope - done, Blocked: 4, Canceled: 3 * i, PlansDone: 2 + 3*i})
		activity = append(activity, ActivityCount{Start: start, End: end, Created: 240, Claimed: 200 + i, Done: 250, Blocked: 2})
	}
	return Report{
		Schema: ReportSchema,
		Window: ReportWindow{Since: since, Until: series[3].End, Bucket: BucketWeek, Timezone: "UTC"},
		Leaves: LeafFigures{Created: 1120, Done: 1070, Canceled: 12, Open: 38, Blocked: 4},
		Plans:  PlanFigures{Imported: 14, Closed: 11, Open: 3, MedianImportToCloseSeconds: new(int64(6*86400 + 4*3600))},
		Pace: PaceFigures{
			DonePerWeek:                31.25,
			MedianCreatedToDoneSeconds: new(int64(86400 + 2*3600)),
			MedianClaimedToDoneSeconds: new(int64(42 * 60)),
		},
		DoneByActor: []ActorCount{{Actor: "claude", Done: 812}, {Actor: "ben", Done: 201}, {Actor: "stats-cli-agent", Done: 57}},
		Series:      series,
		Activity:    activity,
		Imports:     []ImportMarker{{At: since.Add(time.Hour), TaskID: "LfUoov", Title: "Reporting", Source: "plan.md"}},
	}
}

func renderText(t *testing.T, r Report, width int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteReport(&buf, r, ReportFormatText, width); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	return buf.String()
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
}

func TestRenderReport_LeavesLine(t *testing.T) {
	out := renderText(t, sampleReport(), 80)
	mustContain(t, out, "Leaves", "created 1,120", "done 1,070", "canceled 12", "open 38", "blocked 4")
}

func TestRenderReport_PlansLine(t *testing.T) {
	out := renderText(t, sampleReport(), 80)
	mustContain(t, out, "Plans", "imported 14", "closed 11", "open 3", "median import→close 6d 4h")
	if strings.Contains(out, "no imports recorded") {
		t.Errorf("a report with imports must not say none were recorded:\n%s", out)
	}
}

func TestRenderReport_PaceLine(t *testing.T) {
	out := renderText(t, sampleReport(), 80)
	mustContain(t, out, "Pace", "31.2 done/week", "median created→done 1d 2h", "claimed→done 42m")
}

func TestRenderReport_DoneByActorWithCaveat(t *testing.T) {
	out := renderText(t, sampleReport(), 80)
	mustContain(t, out, "Done by", "claude 812", "ben 201", "stats-cli-agent 57", "not who did the work")
}

func TestRenderReport_DoneByActorOverflowFolds(t *testing.T) {
	r := sampleReport()
	r.DoneByActor = nil
	for i, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		r.DoneByActor = append(r.DoneByActor, ActorCount{Actor: name, Done: 10 - i})
	}
	out := renderText(t, r, 80)
	mustContain(t, out, "a 10", "e 6", "2 more (9)")
	if strings.Contains(out, "f 5") {
		t.Errorf("actors past the fifth should fold into a count:\n%s", out)
	}
}

func TestRenderReport_HeaderNamesWindowBucketAndZone(t *testing.T) {
	out := renderText(t, sampleReport(), 80)
	first, _, _ := strings.Cut(out, "\n")
	for _, w := range []string{"Aug 31 2026", "Sep 28 2026", "by week", "UTC"} {
		if !strings.Contains(first, w) {
			t.Errorf("header %q missing %q", first, w)
		}
	}
}

func TestRenderReport_ScopeLineOnlyWhenScoped(t *testing.T) {
	r := sampleReport()
	if out := renderText(t, r, 80); strings.Contains(out, "Scope:") {
		t.Errorf("forest report should carry no scope line:\n%s", out)
	}
	r.Window.Scope = "abc12"
	mustContain(t, renderText(t, r, 80), "Scope: abc12")
}

func TestRenderReport_NilMediansRenderAsDash(t *testing.T) {
	r := sampleReport()
	r.Plans.MedianImportToCloseSeconds = nil
	r.Pace.MedianCreatedToDoneSeconds = nil
	r.Pace.MedianClaimedToDoneSeconds = nil
	out := renderText(t, r, 80)
	mustContain(t, out, "median import→close —", "median created→done —", "claimed→done —")
}

func TestRenderReport_ZeroImportsSaysSoPlainly(t *testing.T) {
	r := sampleReport()
	r.Plans.Imported = 0
	r.Plans.MedianImportToCloseSeconds = nil
	r.Imports = nil
	r.Plans.FirstImportAt = nil
	mustContain(t, renderText(t, r, 80), "no imports recorded — job import records them from 2026-09-26")
}

func TestRenderReport_ZeroImportsInWindowWhenTheStoreHasSome(t *testing.T) {
	r := sampleReport()
	r.Plans.Imported = 0
	first := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	r.Plans.FirstImportAt = &first
	out := renderText(t, r, 80)
	mustContain(t, out, "no imports in this window")
	if strings.Contains(out, "no imports recorded") {
		t.Errorf("a store with imports elsewhere should not say none were recorded:\n%s", out)
	}
}

func TestRenderReport_EmptyReport(t *testing.T) {
	out := renderText(t, Report{Schema: ReportSchema}, 80)
	mustContain(t, out, "Leaves", "created 0", "Plans", "Pace", "0.0 done/week", "Done by", "none in this window", "nothing in scope")
	for _, glyph := range []string{glyphDone, glyphOpen, glyphBlocked} {
		if strings.Contains(out, glyph) {
			t.Errorf("empty report drew chart glyph %q:\n%s", glyph, out)
		}
	}
}

func TestRenderReport_EndsWithNewline(t *testing.T) {
	out := renderText(t, sampleReport(), 80)
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("output should end with exactly one newline: %q", out)
	}
}

func TestFormatSpan(t *testing.T) {
	cases := map[int64]string{
		0:                   "<1m",
		59:                  "<1m",
		60:                  "1m",
		42 * 60:             "42m",
		3600:                "1h",
		3*3600 + 12*60:      "3h 12m",
		86400:               "1d",
		6*86400 + 4*3600:    "6d 4h",
		40*86400 + 30*60:    "40d",
		86400 + 2*3600 + 59: "1d 2h",
	}
	for in, want := range cases {
		if got := formatSpan(in); got != want {
			t.Errorf("formatSpan(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestParseReportFormat(t *testing.T) {
	for raw, want := range map[string]ReportFormat{"md": ReportFormatText, "json": ReportFormatJSON, "csv": ReportFormatCSV, " CSV ": ReportFormatCSV} {
		got, ok := ParseReportFormat(raw)
		if !ok || got != want {
			t.Errorf("ParseReportFormat(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	}
	if _, ok := ParseReportFormat("yaml"); ok {
		t.Error("ParseReportFormat(yaml) should refuse")
	}
}

func TestWriteReport_UnknownFormatErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReport(&buf, sampleReport(), ReportFormat("yaml"), 80); err == nil {
		t.Error("want an error for an unknown format")
	}
}
