package main

import (
	"bytes"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

func TestTermWidth_ColumnsWins(t *testing.T) {
	t.Setenv("COLUMNS", "57")
	if got := termWidth(&bytes.Buffer{}); got != 57 {
		t.Errorf("termWidth = %d, want 57 from $COLUMNS", got)
	}
}

func TestTermWidth_NotATerminalFallsBack(t *testing.T) {
	t.Setenv("COLUMNS", "")
	if got := termWidth(&bytes.Buffer{}); got != job.DefaultReportWidth {
		t.Errorf("termWidth = %d, want the default %d", got, job.DefaultReportWidth)
	}
}

func TestTermWidth_GarbageColumnsIgnored(t *testing.T) {
	t.Setenv("COLUMNS", "wide")
	if got := termWidth(&bytes.Buffer{}); got != job.DefaultReportWidth {
		t.Errorf("termWidth = %d, want the default %d", got, job.DefaultReportWidth)
	}
}
