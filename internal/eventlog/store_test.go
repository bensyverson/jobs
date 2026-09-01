package eventlog

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStorePaths(t *testing.T) {
	cache := filepath.Join("/repo", ".jobs.db")
	if got, want := StoreDir(cache), filepath.Join("/repo", ".jobs"); got != want {
		t.Errorf("StoreDir = %q, want %q", got, want)
	}
	if got, want := LockPath(cache), "/repo/.jobs.db.lock"; got != want {
		t.Errorf("LockPath = %q, want %q", got, want)
	}
	store := StoreDir(cache)
	if got, want := LogDir(store), filepath.Join("/repo", ".jobs", "log"); got != want {
		t.Errorf("LogDir = %q, want %q", got, want)
	}
	if got, want := LogPath(store, "k7Qx2m"), filepath.Join("/repo", ".jobs", "log", "k7Qx2m.jsonl"); got != want {
		t.Errorf("LogPath = %q, want %q", got, want)
	}
}

func TestAcquireLockSerializesHolders(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")

	first, err := AcquireLock(cache)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		second, err := AcquireLock(cache)
		if err != nil {
			t.Errorf("second AcquireLock: %v", err)
			close(acquired)
			return
		}
		close(acquired)
		_ = second.Release()
	}()

	select {
	case <-acquired:
		t.Fatal("second AcquireLock returned while the first lock was held")
	case <-time.After(150 * time.Millisecond):
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second AcquireLock never returned after the first was released")
	}
}

func TestLockGuardsACriticalSection(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")

	var inside int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			l, err := AcquireLock(cache)
			if err != nil {
				t.Errorf("AcquireLock: %v", err)
				return
			}
			mu.Lock()
			inside++
			n := inside
			mu.Unlock()
			if n != 1 {
				t.Errorf("%d holders inside the lock at once", n)
			}
			time.Sleep(time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			if err := l.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		})
	}
	wg.Wait()
}
