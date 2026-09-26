package server_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/server"
)

func TestListen_BindsToRequestedAddr(t *testing.T) {
	srv, ln, err := server.Listen(context.Background(), server.Config{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if srv == nil {
		t.Fatal("Listen: nil server")
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Listen: addr %v is not TCP", ln.Addr())
	}
	if !tcp.IP.IsLoopback() {
		t.Errorf("Listen: want loopback, got %v", tcp.IP)
	}
	if tcp.Port == 0 {
		t.Error("Listen: want bound port, got 0")
	}
}

func TestListen_BindError(t *testing.T) {
	_, _, err := server.Listen(context.Background(), server.Config{Addr: "127.0.0.1:not-a-port"})
	if err == nil {
		t.Fatal("Listen: expected error for malformed addr, got nil")
	}
}

func TestListenWalk_HappyPath(t *testing.T) {
	// A free port binds on the first try.
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("seed listen: %v", err)
	}
	port := tmp.Addr().(*net.TCPAddr).Port
	if err := tmp.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	ln, err := server.ListenWalk(addr, 5)
	if err != nil {
		t.Fatalf("ListenWalk(%q, 5): %v", addr, err)
	}
	defer ln.Close()
	got := ln.Addr().(*net.TCPAddr).Port
	if got != port {
		t.Errorf("port = %d, want first-try %d", got, port)
	}
}

func TestListenWalk_WalksPastInUsePort(t *testing.T) {
	// Pre-bind two adjacent ports; ListenWalk should land on port+2.
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("seed first: %v", err)
	}
	defer first.Close()
	port := first.Addr().(*net.TCPAddr).Port

	second, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port+1)))
	if err != nil {
		// Race: another process grabbed port+1. Skip — the walk-past
		// behavior is still correct, we just can't assert the exact
		// landing port here.
		t.Skipf("could not seed adjacent port %d: %v", port+1, err)
	}
	defer second.Close()

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	ln, err := server.ListenWalk(addr, 5)
	if err != nil {
		t.Fatalf("ListenWalk(%q, 5): %v", addr, err)
	}
	defer ln.Close()
	got := ln.Addr().(*net.TCPAddr).Port
	if got != port+2 {
		t.Errorf("walked to port %d, want %d (skipped two in-use ports)", got, port+2)
	}
}

func TestListenWalk_ExhaustsAfterMaxAttempts(t *testing.T) {
	// Pre-bind a contiguous range and ask for one fewer attempt than
	// the range; ListenWalk must surface an error rather than walk
	// indefinitely.
	const bound = 3
	listeners := make([]net.Listener, 0, bound)
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("seed first: %v", err)
	}
	listeners = append(listeners, first)
	port := first.Addr().(*net.TCPAddr).Port
	for i := 1; i < bound; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port+i)))
		if err != nil {
			t.Skipf("could not seed contiguous port %d: %v", port+i, err)
		}
		listeners = append(listeners, ln)
	}
	defer func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}()

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	if _, err := server.ListenWalk(addr, bound); err == nil {
		t.Fatalf("ListenWalk(%q, %d): expected error after exhausting attempts", addr, bound)
	}
}

func TestListenWalk_DoesNotWalkOnNonAddrInUseError(t *testing.T) {
	// Malformed addr — no walking, surface the parse error directly.
	_, err := server.ListenWalk("127.0.0.1:not-a-port", 20)
	if err == nil {
		t.Fatal("ListenWalk: expected error on malformed addr, got nil")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestServe_RespondsToRequests(t *testing.T) {
	// Home queries the DB for signal cards, so the server test needs
	// a real, migrated database — not a nil handle.
	db, err := job.CreateDB(filepath.Join(t.TempDir(), "serve.db"))
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv, ln, err := server.Listen(context.Background(), server.Config{
		Addr: "127.0.0.1:0",
		DB:   db,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, srv, ln) }()

	url := "http://" + ln.Addr().String() + "/"
	resp, err := httpGet(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /: status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /: Content-Type %q, want text/html", ct)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after context cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of context cancel")
	}
}

func TestServe_ShutsDownOnContextCancel(t *testing.T) {
	srv, ln, err := server.Listen(context.Background(), server.Config{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, srv, ln) }()

	// Give Serve a moment to enter its select.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: got %v, want nil on graceful shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not shut down within 2s")
	}
}

func httpGet(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// TestMux_FullRoutingMatrix spins up the real mux via NewMux and
// verifies the shape of the response for every route-class: root,
// content pages, unknown paths, fingerprinted static assets. It's
// the integration-level companion to the handler-package unit tests.
func TestMux_FullRoutingMatrix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routing.db")
	db, err := job.CreateDB(path)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	defer db.Close()

	mux := server.NewMux(context.Background(), server.Config{DB: db})

	type check struct {
		name        string
		method      string
		path        string
		wantStatus  int
		wantHTML    bool
		containsAny []string
	}
	cases := []check{
		{
			name:        "root renders Home",
			method:      "GET",
			path:        "/",
			wantStatus:  200,
			wantHTML:    true,
			containsAny: []string{"Home · Jobs"},
		},
		{
			name:        "log view renders",
			method:      "GET",
			path:        "/log",
			wantStatus:  200,
			wantHTML:    true,
			containsAny: []string{"c-filter-bar", `class="c-log"`},
		},
		{
			name:        "home panel fragment renders",
			method:      "GET",
			path:        "/home/panel?range=1d",
			wantStatus:  200,
			wantHTML:    true,
			containsAny: []string{"<chart-panel"},
		},
		{
			name:        "preview catalog renders",
			method:      "GET",
			path:        "/preview",
			wantStatus:  200,
			wantHTML:    true,
			containsAny: []string{"Chart panel"},
		},
		{
			name:        "preview state renders",
			method:      "GET",
			path:        "/preview/chart-panel/crowded",
			wantStatus:  200,
			wantHTML:    true,
			containsAny: []string{"<chart-panel"},
		},
		{
			name:        "unknown path returns templated 404",
			method:      "GET",
			path:        "/nope",
			wantStatus:  404,
			wantHTML:    true,
			containsAny: []string{"Error 404", "Page not found"},
		},
		{
			name:        "unknown task id returns templated 404",
			method:      "GET",
			path:        "/tasks/zzzzz",
			wantStatus:  404,
			wantHTML:    true,
			containsAny: []string{"Task not found"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != c.wantStatus {
				t.Errorf("%s %s: status %d, want %d", c.method, c.path, w.Code, c.wantStatus)
			}
			if c.wantHTML {
				ct := w.Header().Get("Content-Type")
				if !strings.HasPrefix(ct, "text/html") {
					t.Errorf("%s %s: Content-Type %q, want text/html", c.method, c.path, ct)
				}
			}
			body := w.Body.String()
			for _, needle := range c.containsAny {
				if !strings.Contains(body, needle) {
					t.Errorf("%s %s: body missing %q\n---\n%s", c.method, c.path, needle, body)
				}
			}
		})
	}
}

func TestMux_StaticAssetsServedWithImmutableCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "static.db")
	db, err := job.CreateDB(path)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	defer db.Close()

	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	tokensURL := m.URL("css/tokens.css")
	if tokensURL == "" {
		t.Fatal("manifest missing tokens.css")
	}

	mux := server.NewMux(context.Background(), server.Config{DB: db})

	req := httptest.NewRequest("GET", tokensURL, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("GET %s: status %d, want 200", tokensURL, w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("GET %s: Cache-Control %q, want immutable", tokensURL, cc)
	}
}

// Compile-time check that DefaultAddr is loopback — a regression fence
// against someone quietly changing the default to 0.0.0.0.
func TestDefaultAddr_IsLoopback(t *testing.T) {
	host, _, err := net.SplitHostPort(server.DefaultAddr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", server.DefaultAddr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Errorf("DefaultAddr host = %q, want a loopback IP", host)
	}
}

// The preview mux is the zero-config entry: no database at all, the
// catalog and its assets only, and the root sends you to the catalog.
func TestPreviewMux_ServesTheCatalogWithoutADatabase(t *testing.T) {
	mux := server.NewPreviewMux()

	req := httptest.NewRequest("GET", "/preview/chart-panel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<chart-panel") {
		t.Fatalf("GET /preview/chart-panel: status %d\n%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 302 || w.Header().Get("Location") != "/preview" {
		t.Errorf("GET /: status %d Location %q, want a redirect to /preview", w.Code, w.Header().Get("Location"))
	}
}
