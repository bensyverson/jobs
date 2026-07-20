package job

import (
	"testing"
	"time"
)

func TestParseSince_EmptyReturnsNil(t *testing.T) {
	got, err := ParseSince("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestParseSince_RelativeDuration(t *testing.T) {
	before := time.Now().Unix()
	got, err := ParseSince("2h")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want non-nil")
	}
	wantApprox := before - 2*3600
	if *got < wantApprox-5 || *got > wantApprox+5 {
		t.Errorf("got %d, want ~%d", *got, wantApprox)
	}
}

func TestParseSince_RFC3339(t *testing.T) {
	got, err := ParseSince("2026-04-28T10:00:00Z")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want non-nil")
	}
	want := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC).Unix()
	if *got != want {
		t.Errorf("got %d, want %d", *got, want)
	}
}

func TestParseSince_Invalid(t *testing.T) {
	if _, err := ParseSince("not-a-time"); err == nil {
		t.Fatal("expected error, got nil")
	}
}
