package main

import (
	"io"
	"os"
	"strconv"

	job "github.com/bensyverson/jobs/internal/job"
)

// termWidth is the column count text output should fit: $COLUMNS when
// it holds a positive number, else the width of w when w is a terminal,
// else job.DefaultReportWidth (a pipe or a file has no width).
func termWidth(w io.Writer) int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	if f, ok := w.(*os.File); ok {
		if n, ok := fileWidth(f); ok && n > 0 {
			return n
		}
	}
	return job.DefaultReportWidth
}
