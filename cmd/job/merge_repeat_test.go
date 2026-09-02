package main

import (
	"strings"
	"testing"
)

// Merging the same pair twice succeeds and writes nothing. The second run goes
// through a real `job` invocation, so the adoption the first merge triggered
// has happened by the time it reads the local events table.
func TestMergeCLI_SecondMergeSaysAlreadyMerged(t *testing.T) {
	localPath, otherPath, _, _ := mergeCLIPair(t)

	if out, _, err := runCLI(t, localPath, "merge", otherPath); err != nil {
		t.Fatalf("first merge: %v\n%s", err, out)
	}

	out, _, err := runCLI(t, localPath, "merge", otherPath)
	if err != nil {
		t.Fatalf("second merge of the same pair should exit 0, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Already merged") || !strings.Contains(out, "nothing to do") {
		t.Errorf("report should say the pair is already merged:\n%s", out)
	}
}
