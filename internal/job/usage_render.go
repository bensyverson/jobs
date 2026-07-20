package job

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// RenderUsage writes the human-readable md activity report. Zero-count
// statuses are omitted from the status row per the v1 design. The output
// is deliberately compact: one header line, one status row, one rates
// row, and an activity block. Identity and strict-mode lines are NOT
// emitted — this is an observe-only report, not the DB-wide briefing.
func RenderUsage(w io.Writer, u *Usage) {
	windowLabel := usageHeaderLabel(u)
	fmt.Fprintf(w, "Usage (%s)\n", windowLabel)

	statusParts := make([]string, 0, 5)
	if u.Open > 0 {
		statusParts = append(statusParts, fmt.Sprintf("open %d", u.Open))
	}
	if u.Claimed > 0 {
		statusParts = append(statusParts, fmt.Sprintf("claimed %d", u.Claimed))
	}
	if u.Done > 0 {
		statusParts = append(statusParts, fmt.Sprintf("done %d", u.Done))
	}
	if u.Canceled > 0 {
		statusParts = append(statusParts, fmt.Sprintf("canceled %d", u.Canceled))
	}
	if u.Blocked > 0 {
		statusParts = append(statusParts, fmt.Sprintf("blocked %d", u.Blocked))
	}
	if len(statusParts) > 0 {
		fmt.Fprintf(w, "  %s\n", strings.Join(statusParts, " · "))
	}

	// Rates row.
	totalClosed := u.Done + u.Canceled
	if totalClosed > 0 {
		rateParts := make([]string, 0, 2)
		if u.Done > 0 {
			rateParts = append(rateParts, fmt.Sprintf("completion %d%%", percent(u.Done, totalClosed)))
		}
		if u.Canceled > 0 {
			rateParts = append(rateParts, fmt.Sprintf("cancellation %d%%", percent(u.Canceled, totalClosed)))
		}
		if len(rateParts) > 0 {
			fmt.Fprintf(w, "  %s\n", strings.Join(rateParts, " · "))
		}
	}

	// Activity block.
	if u.EventCount > 0 || u.VelocityRate > 0 || u.DBFileSizeBytes > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Activity")
	}

	if u.EventCount > 0 {
		var eventParts []string
		eventParts = append(eventParts, fmt.Sprintf("events %s", formatEventCount(u.EventCount)))
		eventParts = append(eventParts, fmt.Sprintf("first %s", formatUnixDate(u.FirstEventAt)))
		if u.LastEventAt > 0 {
			eventParts = append(eventParts, fmt.Sprintf("last %s ago", formatAgo(u.LastEventAt)))
		}
		fmt.Fprintf(w, "  %s\n", strings.Join(eventParts, " · "))
	}

	if u.VelocityRate > 0 {
		fmt.Fprintf(w, "  velocity %s/day (over %s)\n",
			formatVelocityRate(u.VelocityRate),
			formatDenomDays(u.VelocityDenominatorDays),
		)
	}

	if u.DBFileSizeBytes > 0 {
		fmt.Fprintf(w, "  db %s\n", formatBytes(u.DBFileSizeBytes))
	}
}

func usageHeaderLabel(u *Usage) string {
	if u.WindowKind == "windowed" {
		days := int(math.Round(u.WindowDays))
		if days > 0 {
			return fmt.Sprintf("last %dd", days)
		}
		// Very short window (<1d): render in hours for precision.
		return "this window"
	}
	return "all-time"
}

func percent(num, denom int) int {
	if denom == 0 {
		return 0
	}
	return int(math.Round(float64(num) / float64(denom) * 100.0))
}

// formatEventCount prints thousand-separated counts.
func formatEventCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	// crude 3-digit grouping, since we only used commas here.
	return insertCommas(s)
}

func insertCommas(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	first := n % 3
	if first == 0 {
		first = 3
	}
	var b strings.Builder
	b.WriteString(s[:first])
	for i := first; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// formatBytes prints humanized file sizes. We pick the largest unit where
// the value is ≥1, e.g. 422 KB, 1.4 MB, 2 GB.
func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%d KB", int(math.Round(float64(n)/float64(KB))))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func formatUnixDate(unix int64) string {
	if unix <= 0 {
		return "—"
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02")
}

func formatAgo(unix int64) string {
	now := CurrentNowFunc().Unix()
	if unix <= 0 {
		return "—"
	}
	ago := max(now-unix, 0)
	return FormatDuration(ago)
}

func formatVelocityRate(rate float64) string {
	// Always show at least one decimal, e.g. 3.2 rather than 3.
	return trimTrailingZeroes(fmt.Sprintf("%.2f", rate))
}

func formatDenomDays(days float64) string {
	rounded := max(int(math.Round(days)), 1)
	return fmt.Sprintf("%dd", rounded)
}

func trimTrailingZeroes(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
