package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/status"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/update"
)

const (
	updateEvery     = 6 * time.Hour
	releasesPageURL = "https://github.com/CosmicDriftGameStudio/kumiko-satellite/releases/latest"
)

// pendingRestart holds the freshly installed executable; main starts it after
// realMain returned, when the old status listener and session file are closed.
var pendingRestart atomic.Pointer[string]

type updater struct {
	checker update.Checker
	version string
	ctx     context.Context
	quit    func()
	exePath func() (string, error)
	running atomic.Bool

	mu  sync.Mutex
	rel update.Release
	has bool
}

// startUpdates cleans up after the previous update and checks for a new
// release now and every few hours. Errors are only logged. Only the window
// mode installs by itself (selfUpdate); headless runs and macOS link to the release page.
func startUpdates(ctx context.Context, quit func(), selfUpdate bool) *status.UpdateOptions {
	if exe, err := os.Executable(); err == nil {
		update.CleanupOld(exe)
	}
	u := &updater{version: version, ctx: ctx, quit: quit, exePath: os.Executable}
	go u.loop()
	opt := &status.UpdateOptions{Available: u.available, PageURL: releasesPageURL}
	if selfUpdate && runtime.GOOS != "darwin" {
		opt.Start = u.start
	}
	return opt
}

func (u *updater) loop() {
	for {
		rel, ok, err := u.checker.Check(u.ctx, u.version)
		if err != nil {
			log.Printf("update check: %v", err)
		} else {
			u.mu.Lock()
			u.rel, u.has = rel, ok
			u.mu.Unlock()
		}
		select {
		case <-u.ctx.Done():
			return
		case <-time.After(updateEvery):
		}
	}
}

func (u *updater) available() (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.rel.Version, u.has
}

func (u *updater) start() error {
	if !u.running.CompareAndSwap(false, true) {
		return errors.New("update already running")
	}
	u.mu.Lock()
	rel, has := u.rel, u.has
	u.mu.Unlock()
	if !has {
		u.running.Store(false)
		return errors.New("no update available")
	}
	exe, err := u.exePath()
	if err != nil {
		u.running.Store(false)
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := u.checker.Apply(u.ctx, rel, exe); err != nil {
		log.Printf("update to %s failed: %v", rel.Version, err)
		u.running.Store(false)
		return err
	}
	log.Printf("updated to %s, restarting", rel.Version)
	pendingRestart.Store(&exe)
	u.quit()
	return nil
}

// restartIfUpdated starts the installed executable with the original arguments.
func restartIfUpdated() {
	exe := pendingRestart.Load()
	if exe == nil {
		return
	}
	if err := exec.Command(*exe, os.Args[1:]...).Start(); err != nil {
		log.Printf("restart after update failed: %v", err)
	}
}
