package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/session"
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
	sessionPath, err := session.Path()
	if err != nil {
		return err
	}
	if signalRunning(sessionPath) == nil {
		return nil // an instance of this user runs and shows its window again
	}
	ln, info, err := listenWithSession(defaultStatusAddr, sessionPath)
	if err != nil {
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

	nonces := status.NewNonces()
	loginURL := func() string {
		nonce, err := nonces.New()
		if err != nil {
			log.Printf("login nonce: %v", err)
		}
		return info.LoginURL(nonce)
	}
	handler, err := status.NewHandler(status.Options{
		Version: version, Source: r.source, Wizard: wiz, Settings: r, AppURL: r.AppURL,
		ShowWindow: func() { requestWindow(loginURL) },
		Secret:     info.Secret, Nonces: nonces,
	})
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	log.Printf("status page: http://%s/", info.Addr())
	requestWindow(loginURL)
	<-ctx.Done()
	return context.Canceled
}
