package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/setup"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/status"
)

const (
	defaultStatusAddr = "127.0.0.1:8765"
	maxLogBytes       = 1 << 20
)

// setupLog mirrors log output into a file next to state.json: the Windows exe
// has no console when started by double click.
func setupLog() {
	dir, err := config.Dir()
	if err != nil || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	path := filepath.Join(dir, "kumiko-satellite.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogBytes {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

// gui is the double-click mode: local page for setup and status, sync in this process.
func gui(ctx context.Context) error {
	addr := defaultStatusAddr
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if status.SignalShow(addr) {
			return nil
		}
		if status.IsSatellite(addr) {
			return desktop.OpenBrowser("http://" + addr + "/")
		}
		return err
	}
	defer ln.Close()

	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	statePath, err := config.StatePath()
	if err != nil {
		return err
	}
	r := &runner{parent: ctx, configPath: cfgPath, statePath: statePath}
	defer r.stop()
	wiz := setup.New(ctx, r.paired, r.SetFolder)

	switch cfg, err := config.Load(cfgPath); {
	case err != nil:
		wiz.Open("")
	case cfg.Token == "":
		wiz.Open(cfg.URL)
	case cfg.Folder == "":
		r.adopt(cfg)
		wiz.OpenFolder(cfg.URL, "")
	default:
		if err := r.start(cfg); err != nil {
			log.Printf("saved folder unusable: %v", err)
			r.adopt(cfg)
			wiz.OpenFolder(cfg.URL, "")
		}
	}

	pageURL := "http://" + ln.Addr().String() + "/"
	handler, err := status.NewHandler(status.Options{
		Version: version, Source: r.source, Wizard: wiz, Settings: r, AppURL: r.AppURL,
		ShowWindow: func() { requestWindow(pageURL) },
	})
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	log.Printf("status page: %s", pageURL)
	requestWindow(pageURL)
	<-ctx.Done()
	return context.Canceled
}
