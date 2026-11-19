package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/session"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/status"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

type noSource struct{}

func (noSource) Snapshot() syncer.Snapshot { return syncer.Snapshot{} }
func (noSource) Retry()                    {}

func TestSecondInstanceWithoutSessionFileSendsNothing(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(srv.Close)
	if err := signalRunning(filepath.Join(t.TempDir(), "session")); err == nil {
		t.Fatal("a missing session file must be an error")
	}
	if requests.Load() != 0 {
		t.Fatal("no request may be sent without a readable session file")
	}
}

func TestSecondInstanceSignalsViaSessionFile(t *testing.T) {
	shown := 0
	h, err := status.NewHandler(status.Options{
		Version: "1", Secret: "s3cret", Port: 8765, Source: func() status.Source { return noSource{} }, ShowWindow: func() { shown++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	path := filepath.Join(t.TempDir(), "session")
	if err := session.Write(path, session.Info{Port: port, Secret: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	if err := signalRunning(path); err != nil || shown != 1 {
		t.Fatalf("signal: %v, shown %d", err, shown)
	}
	if err := session.Write(path, session.Info{Port: port, Secret: "stale"}); err != nil {
		t.Fatal(err)
	}
	if err := signalRunning(path); err == nil || shown != 1 {
		t.Fatalf("a stale secret must fail: %v, shown %d", err, shown)
	}
}

func TestTakenPortFallsBackToFreeOneWithOwnSecret(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { taken.Close() })
	path := filepath.Join(t.TempDir(), "session")
	ln, info, err := listenWithSession(taken.Addr().String(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	if info.Port == taken.Addr().(*net.TCPAddr).Port || info.Secret == "" {
		t.Fatalf("info %+v", info)
	}
	if got, err := session.Read(path); err != nil || got != info {
		t.Fatalf("session file %+v, %v", got, err)
	}
}

func TestOpenWithForeignServerEndsInError(t *testing.T) {
	other, err := status.NewHandler(status.Options{Version: "1", Secret: "not-ours", Port: 8765, Source: func() status.Source { return noSource{} }})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		other.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	path, err := session.Path()
	if err != nil {
		t.Fatal(err)
	}
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	if err := session.Write(path, session.Info{Port: port, Secret: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	opened := false
	old := desktop.OpenBrowser
	desktop.OpenBrowser = func(string) error { opened = true; return nil }
	t.Cleanup(func() { desktop.OpenBrowser = old })
	if err := openCommand(); !errors.Is(err, status.ErrForeignServer) || opened {
		t.Fatalf("open: %v, browser opened %v", err, opened)
	}
	if len(paths) != 1 || paths[0] != "/hello" {
		t.Fatalf("requests to the foreign server: %v", paths)
	}
}
