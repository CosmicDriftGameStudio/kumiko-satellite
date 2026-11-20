package main

import (
	"context"
	"sync"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/api"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/filter"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/status"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

// runner owns the running sync engine so pairing and settings can replace it
// inside the same process.
type runner struct {
	parent     context.Context
	configPath string
	statePath  string

	mu     sync.Mutex
	cfg    config.Config
	engine *syncer.Engine
	cancel context.CancelFunc
	done   chan struct{}
}

// start (re)starts the sync with cfg after saving it.
func (r *runner) start(cfg config.Config) error {
	if err := filter.CheckFolder(cfg.Folder); err != nil {
		return err
	}
	client, err := api.New(cfg.URL, cfg.Token)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	if err := config.Save(r.configPath, cfg); err != nil {
		return err
	}
	engine := syncer.New(cfg.Folder, r.statePath, client)
	ctx, cancel := context.WithCancel(r.parent)
	done := make(chan struct{})
	r.cfg, r.engine, r.cancel, r.done = cfg, engine, cancel, done
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); engine.RunHeartbeat(ctx, client, version, heartbeatEvery) }()
	go func() { defer wg.Done(); engine.Run(ctx, scanEvery) }()
	go func() { wg.Wait(); close(done) }()
	return nil
}

func (r *runner) stopLocked() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.cancel, r.engine = nil, nil
}

func (r *runner) source() status.Source {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.engine == nil {
		return nil
	}
	return r.engine
}

func (r *runner) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
}

// paired takes the new token. Without a folder the config is only saved (step
// 2 of the setup still has to choose one); with a folder the sync restarts.
func (r *runner) paired(cfg config.Config) error {
	if cfg.Folder != "" {
		return r.start(cfg)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	if err := config.Save(r.configPath, cfg); err != nil {
		return err
	}
	r.cfg = cfg
	return nil
}

// adopt remembers a saved config without starting the sync.
func (r *runner) adopt(cfg config.Config) {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
}

// AppURL, Folder, DeviceName, SetFolder, Autostart and SetAutostart implement status.Settings.
func (r *runner) AppURL() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.URL
}

func (r *runner) Folder() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.Folder
}

func (r *runner) DeviceName() string {
	r.mu.Lock()
	engine := r.engine
	r.mu.Unlock()
	if engine == nil {
		return ""
	}
	return engine.DeviceName()
}

func (r *runner) SetFolder(folder string) error {
	r.mu.Lock()
	cfg := r.cfg
	r.mu.Unlock()
	cfg.Folder = folder
	return r.start(cfg)
}

func (r *runner) Autostart() bool            { return desktop.AutostartEnabled() }
func (r *runner) SetAutostart(on bool) error { return desktop.SetAutostart(on) }
