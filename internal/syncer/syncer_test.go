package syncer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/api"
)

type harness struct {
	t      *testing.T
	dir    string
	e      *Engine
	status atomic.Int32 // answer of /v1/files
	files  atomic.Int32
	retry  atomic.Bool // heartbeat answer
	clock  time.Time
	url    string
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, dir: t.TempDir(), clock: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)}
	h.status.Store(201)
	mux := http.NewServeMux()
	mux.HandleFunc("/connectors/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ksat_t" || r.Header.Get("X-Kumiko-Sha256") == "" {
			t.Errorf("bad upload request headers")
		}
		h.files.Add(1)
		code := int(h.status.Load())
		if code == 429 {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(code)
		switch code {
		case 507:
			_, _ = w.Write([]byte(`{"error":"storage_quota_exceeded"}`))
		case 201:
			_, _ = w.Write([]byte(`{"id":"1","duplicate":false}`))
		}
	})
	mux.HandleFunc("/connectors/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if h.retry.Load() {
			_, _ = w.Write([]byte(`{"ok":true,"retry":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	h.url = srv.URL
	client, err := api.New(srv.URL, "ksat_t")
	if err != nil {
		t.Fatal(err)
	}
	h.e = New(h.dir, filepath.Join(t.TempDir(), "state.json"), client)
	h.e.now = func() time.Time { return h.clock }
	return h
}

func (h *harness) write(rel, content string) {
	p := filepath.Join(h.dir, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// cycle runs the two scans that make a file stable, then uploads.
func (h *harness) cycle() {
	for range 2 {
		if err := h.e.Scan(); err != nil {
			h.t.Fatal(err)
		}
	}
	h.e.Process(context.Background())
}

func TestFileNeedsTwoScansAndIsUploadedOnce(t *testing.T) {
	h := newHarness(t)
	h.write("a.txt", "hello")
	h.write(".hidden", "x")
	h.write("Thumbs.db", "x")
	_ = h.e.Scan()
	h.e.Process(context.Background())
	if h.files.Load() != 0 {
		t.Fatal("a file seen once must not be uploaded")
	}
	if s := h.e.Snapshot(); s.State != StateChecking || s.Checking != 1 {
		t.Fatalf("file in stability check: %+v", s)
	}
	_ = h.e.Scan()
	h.e.Process(context.Background())
	if h.files.Load() != 1 {
		t.Fatalf("uploads = %d, want 1 (hidden and ballast skipped)", h.files.Load())
	}
	h.cycle()
	if h.files.Load() != 1 {
		t.Fatal("an unchanged file must not be uploaded again")
	}
	if s := h.e.Snapshot(); s.State != StateIdle || s.Done != 1 || s.Total != 1 {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestStateIsCheckingUntilTheFirstScanFinished(t *testing.T) {
	h := newHarness(t)
	h.write("a.txt", "hello")
	if s := h.e.Snapshot(); s.State != StateChecking {
		t.Fatalf("before the first scan: %+v", s)
	}
	_ = h.e.Scan()
	h.e.Process(context.Background())
	if s := h.e.Snapshot(); s.State != StateChecking {
		t.Fatalf("file still in stability check: %+v", s)
	}
	h.cycle()
	if s := h.e.Snapshot(); s.State != StateIdle {
		t.Fatalf("after uploading: %+v", s)
	}
	empty := newHarness(t)
	_ = empty.e.Scan()
	if s := empty.e.Snapshot(); s.State != StateIdle {
		t.Fatalf("an empty folder is up to date once scanned: %+v", s)
	}
}

func TestSymlinkIsNotFollowed(t *testing.T) {
	h := newHarness(t)
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644)
	if err := os.Symlink(outside, filepath.Join(h.dir, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	h.cycle()
	if h.files.Load() != 0 {
		t.Fatal("symlinked folder must not be followed")
	}
}

func TestFileSwappedForLinkAfterScanIsNotRead(t *testing.T) {
	h := newHarness(t)
	h.write("a.txt", "1")
	for range 2 {
		if err := h.e.Scan(); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outside, []byte("s"), 0o644)
	target := filepath.Join(h.dir, "a.txt")
	_ = os.Remove(target)
	if err := os.Symlink(outside, target); err != nil {
		t.Skip("symlinks unavailable")
	}
	h.e.Process(context.Background())
	if h.files.Load() != 0 {
		t.Fatal("a file replaced by a link to outside the folder must not be uploaded")
	}
}

func TestQuotaBlocksUntilRetry(t *testing.T) {
	h := newHarness(t)
	h.write("a.txt", "1")
	h.write("b.txt", "2")
	h.status.Store(507)
	h.cycle()
	s := h.e.Snapshot()
	if s.State != StateQuota || h.files.Load() != 1 {
		t.Fatalf("state %s, uploads %d: want quota after one attempt", s.State, h.files.Load())
	}
	h.e.Process(context.Background())
	if h.files.Load() != 1 {
		t.Fatal("no uploads while blocked")
	}
	h.status.Store(201)
	h.e.Retry()
	h.e.Process(context.Background())
	if s := h.e.Snapshot(); s.State != StateIdle || s.Done != 2 {
		t.Fatalf("after retry: %+v", s)
	}
}

func TestRevokedTokenShowsPairAgain(t *testing.T) {
	h := newHarness(t)
	h.write("a.txt", "1")
	h.status.Store(401)
	h.cycle()
	if s := h.e.Snapshot(); s.State != StateRevoked || s.LastError != messageRevoked {
		t.Fatalf("%+v", s)
	}
}

func TestBackoffAndRetryAfter(t *testing.T) {
	h := newHarness(t)
	h.write("a.txt", "1")
	h.status.Store(500)
	h.cycle() // fails, retry in 1 min
	h.e.Process(context.Background())
	if h.files.Load() != 1 {
		t.Fatal("must wait for the backoff")
	}
	h.clock = h.clock.Add(61 * time.Second)
	h.status.Store(429)
	h.e.Process(context.Background()) // fails with Retry-After 120 s
	h.clock = h.clock.Add(61 * time.Second)
	h.e.Process(context.Background())
	if h.files.Load() != 2 {
		t.Fatalf("Retry-After of 120 s must be respected, uploads = %d", h.files.Load())
	}
	h.clock = h.clock.Add(61 * time.Second)
	h.status.Store(201)
	h.e.Process(context.Background())
	if s := h.e.Snapshot(); s.State != StateIdle || s.Failed != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestHeartbeatRetryRequeuesFailedFiles(t *testing.T) {
	h := newHarness(t)
	h.write("a.txt", "1")
	h.status.Store(422)
	h.cycle()
	if s := h.e.Snapshot(); s.Failed != 1 {
		t.Fatalf("want 1 failed, got %+v", s)
	}
	client, _ := api.New(h.url, "ksat_t")
	h.status.Store(201)
	h.retry.Store(true)
	h.e.SendHeartbeat(context.Background(), client, "1.0")
	if s := h.e.Snapshot(); s.Failed != 0 || s.Queued != 1 {
		t.Fatalf("heartbeat retry must requeue: %+v", s)
	}
}

func TestEventLogIsRingBuffer(t *testing.T) {
	h := newHarness(t)
	h.e.mu.Lock()
	for range 250 {
		h.e.logLocked("x", false)
	}
	h.e.mu.Unlock()
	if n := len(h.e.Snapshot().Events); n != 200 {
		t.Fatalf("events = %d", n)
	}
}

func TestWireStateIsAcceptedByServer(t *testing.T) {
	for _, st := range []State{StateIdle, StateSyncing, StateError, StateChecking, StateQuota, StateRevoked} {
		switch w := wireState(st); w {
		case "idle", "syncing", "error":
		default:
			t.Errorf("state %q maps to %q, server accepts only idle|syncing|error", st, w)
		}
	}
}
