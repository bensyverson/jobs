package job

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ReportFormat is an output shape of `job stats`.
type ReportFormat string

const (
	// ReportFormatText is the human report: headline figures, then a
	// text burn-up. Its flag value is "md", matching the other verbs'
	// --format, though the output is plain text.
	ReportFormatText ReportFormat = "md"
	// ReportFormatJSON is the Report struct itself, carrying `schema`.
	ReportFormatJSON ReportFormat = "json"
	// ReportFormatCSV is the series in long form, one row per bucket.
	ReportFormatCSV ReportFormat = "csv"
)

var reportFormats = []ReportFormat{ReportFormatText, ReportFormatJSON, ReportFormatCSV}

// ParseReportFormat normalizes a --format value (trimmed,
// case-insensitive); ok is false for anything unknown.
func ParseReportFormat(raw string) (f ReportFormat, ok bool) {
	f = ReportFormat(strings.ToLower(strings.TrimSpace(raw)))
	if slices.Contains(reportFormats, f) {
		return f, true
	}
	return f, false
}

// ReportFormatList is the accepted --format values, for flag help and
// errors.
func ReportFormatList() string {
	names := make([]string, len(reportFormats))
	for i, f := range reportFormats {
		names[i] = string(f)
	}
	return strings.Join(names, ", ")
}

// DefaultReportWidth is the text width assumed when the output is not
// a terminal or its width is unknown.
const DefaultReportWidth = 80

// maxActorsShown is how many identities the Done-by line names before
// folding the rest into a count.
const maxActorsShown = 5

// headlineIndent aligns every headline row's figures after its label.
const headlineIndent = "         "

// WriteReport renders r to w in format f. width is the terminal width
// the text burn-up fits; zero or less means DefaultReportWidth. JSON
// and CSV ignore it.
func WriteReport(w io.Writer, r Report, f ReportFormat, width int) error {
	switch f {
	case ReportFormatText:
		return writeReportText(w, r, width)
	case ReportFormatJSON:
		return writeReportJSON(w, r)
	case ReportFormatCSV:
		return writeReportCSV(w, r)
	default:
		return fmt.Errorf("unknown report format %q (want one of %s)", f, ReportFormatList())
	}
}

func writeReportText(w io.Writer, r Report, width int) error {
	loc := reportLocation(r.Window)
	var b strings.Builder
	b.WriteString(reportHeader(r.Window, loc) + "\n")
	if r.Window.Scope != "" {
		fmt.Fprintf(&b, "Scope: %s\n", r.Window.Scope)
	}
	b.WriteString("\n")

	l := r.Leaves
	headline(&b, "Leaves", fmt.Sprintf("created %s · done %s · canceled %s · open %s · blocked %s",
		commas(l.Created), commas(l.Done), commas(l.Canceled), commas(l.Open), commas(l.Blocked)))

	p := r.Plans
	headline(&b, "Plans", fmt.Sprintf("imported %s · closed %s · open %s · median import→close %s",
		commas(p.Imported), commas(p.Closed), commas(p.Open), medianText(p.MedianImportToCloseSeconds)))
	if p.Imported == 0 {
		b.WriteString(headlineIndent + noImportsNote(r.Plans) + "\n")
	}

	pace := r.Pace
	headline(&b, "Pace", fmt.Sprintf("%.1f done/week · median created→done %s · claimed→done %s",
		pace.DonePerWeek, medianText(pace.MedianCreatedToDoneSeconds), medianText(pace.MedianClaimedToDoneSeconds)))

	if len(r.DoneByActor) == 0 {
		headline(&b, "Done by", "none in this window")
	} else {
		headline(&b, "Done by", actorsText(r.DoneByActor))
		b.WriteString(headlineIndent + "identities that ran `done`, not who did the work\n")
	}

	b.WriteString("\n")
	writeBurnUp(&b, r, width, loc)
	_, err := io.WriteString(w, b.String())
	return err
}

func headline(b *strings.Builder, label, figures string) {
	fmt.Fprintf(b, "%-*s%s\n", len(headlineIndent), label, figures)
}

// reportLocation is the zone the report's times are shown in: the one
// it was bucketed in, so a day label matches the day it counts.
func reportLocation(win ReportWindow) *time.Location {
	if win.Timezone != "" {
		if loc, err := time.LoadLocation(win.Timezone); err == nil {
			return loc
		}
	}
	return win.Until.Location()
}

func reportHeader(win ReportWindow, loc *time.Location) string {
	parts := []string{}
	if !win.Until.IsZero() {
		layout := "Jan 2 2006"
		if win.Bucket == BucketMinute || win.Bucket == BucketFiveMinutes || win.Bucket == BucketHour {
			layout = "Jan 2 2006 15:04"
		}
		parts = append(parts, win.Since.In(loc).Format(layout)+" → "+win.Until.In(loc).Format(layout))
	}
	if win.Bucket != "" {
		parts = append(parts, "by "+string(win.Bucket))
	}
	if win.Timezone != "" {
		parts = append(parts, win.Timezone)
	}
	if len(parts) == 0 {
		return "Stats"
	}
	return "Stats  " + strings.Join(parts, " · ")
}

// noImportsNote explains a zero import count: none in this window, or none
// ever — imported events began with the release that records them, so an
// older store has no trace of its imports (decision 5).
func noImportsNote(p PlanFigures) string {
	if p.FirstImportAt != nil {
		return "no imports in this window"
	}
	return "no imports recorded — job import records them from 2026-09-26"
}

func actorsText(actors []ActorCount) string {
	shown := actors[:min(len(actors), maxActorsShown)]
	parts := make([]string, 0, len(shown)+1)
	for _, a := range shown {
		parts = append(parts, a.Actor+" "+commas(a.Done))
	}
	if rest := actors[len(shown):]; len(rest) > 0 {
		folded := 0
		for _, a := range rest {
			folded += a.Done
		}
		parts = append(parts, fmt.Sprintf("%d more (%s)", len(rest), commas(folded)))
	}
	return strings.Join(parts, " · ")
}

func medianText(seconds *int64) string {
	if seconds == nil {
		return "—"
	}
	return formatSpan(*seconds)
}

// formatSpan renders a duration as its two largest units, rounded
// down: "6d 4h", "3h 12m", "42m". Under a minute is "<1m".
func formatSpan(seconds int64) string {
	const minute, hour, day = 60, 3600, 86400
	switch {
	case seconds < minute:
		return "<1m"
	case seconds < hour:
		return fmt.Sprintf("%dm", seconds/minute)
	case seconds < day:
		return twoUnits(seconds/hour, "h", seconds%hour/minute, "m")
	default:
		return twoUnits(seconds/day, "d", seconds%day/hour, "h")
	}
}

func twoUnits(big int64, bigUnit string, small int64, smallUnit string) string {
	if small == 0 {
		return fmt.Sprintf("%d%s", big, bigUnit)
	}
	return fmt.Sprintf("%d%s %d%s", big, bigUnit, small, smallUnit)
}

// commas renders n with thousands separators.
func commas(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
