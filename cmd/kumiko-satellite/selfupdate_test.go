package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/update"
)

const fixtureBinary = "new-binary-content"

// newTestUpdater serves one release with the given digest of the asset and
// returns an updater that already knows it, plus the temp exe it replaces.
func newTestUpdater(t *testing.T, digest string) (*updater, string, *atomic.Int32) {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v0.3.0","html_url":"https://example.test/rel","assets":[`+
			`{"name":"kumiko-satellite-linux-amd64","browser_download_url":%q,"digest":%q}]}`,
			srv.URL+"/dl/kumiko-satellite-linux-amd64", digest)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(fixtureBinary)) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "kumiko-satellite")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var quits atomic.Int32
	u := &updater{
		checker: update.Checker{BaseURL: srv.URL + "/latest", DownloadPrefix: srv.URL + "/dl/", GOOS: "linux", GOARCH: "amd64"},
		version: "0.2.1", ctx: context.Background(),
		quit:    func() { quits.Add(1) },
		exePath: func() (string, error) { return exe, nil },
	}
	rel, ok, err := u.checker.Check(context.Background(), u.version)
	if err != nil || !ok {
		t.Fatalf("check: ok=%v err=%v", ok, err)
	}
	u.rel, u.has = rel, true
	t.Cleanup(func() { pendingRestart.Store(nil) })
	return u, exe, &quits
}

func digestOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

func TestStartInstallsAndRequestsRestartOnce(t *testing.T) {
	u, exe, quits := newTestUpdater(t, digestOf(fixtureBinary))
	if err := u.start(); err != nil {
		t.Fatal(err)
	}
	got := pendingRestart.Load()
	if got == nil || filepath.Base(*got) != "kumiko-satellite" {
		t.Fatalf("pendingRestart = %v", got)
	}
	if b, _ := os.ReadFile(exe); string(b) != fixtureBinary {
		t.Errorf("exe = %q", b)
	}
	if quits.Load() != 1 {
		t.Errorf("quit called %d times", quits.Load())
	}
	if err := u.start(); err == nil || !strings.Contains(err.Error(), "update already running") {
		t.Errorf("second start: %v", err)
	}
	if quits.Load() != 1 {
		t.Errorf("quit called %d times after second start", quits.Load())
	}
}

func TestStartWithWrongDigestChangesNothing(t *testing.T) {
	u, exe, quits := newTestUpdater(t, digestOf("something else"))
	if err := u.start(); err == nil {
		t.Fatal("wrong digest must fail")
	}
	if pendingRestart.Load() != nil || quits.Load() != 0 {
		t.Error("no restart and no quit after a failed update")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old-binary" {
		t.Errorf("exe changed: %q", b)
	}
	if u.running.Load() {
		t.Error("running must be reset after a failure")
	}
}

func TestStartWithoutUpdateFails(t *testing.T) {
	u, _, _ := newTestUpdater(t, digestOf(fixtureBinary))
	u.has = false
	if err := u.start(); err == nil || !strings.Contains(err.Error(), "no update available") {
		t.Errorf("err = %v", err)
	}
	if u.running.Load() {
		t.Error("running must be reset")
	}
}
