package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/session"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/status"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/update"
)

const (
	updateEvery     = 6 * time.Hour
	releasesPageURL = "https://github.com/CosmicDriftGameStudio/kumiko-satellite/releases/latest"
	// restartedEnv tells the new instance that its predecessor is still shutting down.
	restartedEnv     = "KUMIKO_SATELLITE_RESTARTED"
	restartWaitLimit = 10 * time.Second
)

// pendingRestart holds the freshly installed executable; main starts it after
// the old status listener is closed, or the new instance would see the old one.
var pendingRestart atomic.Pointer[string]

type updater struct {
	checker update.Checker
	version string
	ctx     context.Context
	quit    func()

	mu  sync.Mutex
	rel update.Release
	has bool
}

// startUpdates cleans up after the previous update and checks for a new
// release now and every few hours. Errors are only logged.
func startUpdates(ctx context.Context, quit func()) *status.UpdateOptions {
	if exe, err := os.Executable(); err == nil {
		update.CleanupOld(exe)
	}
	u := &updater{version: version, ctx: ctx, quit: quit}
	go u.loop()
	opt := &status.UpdateOptions{Available: u.available, PageURL: releasesPageURL}
	if runtime.GOOS != "darwin" {
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
	u.mu.Lock()
	rel, has := u.rel, u.has
	u.mu.Unlock()
	if !has {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := u.checker.Apply(u.ctx, rel, exe); err != nil {
		log.Printf("update to %s failed: %v", rel.Version, err)
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
	cmd := exec.Command(*exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), restartedEnv+"=1")
	if err := cmd.Start(); err != nil {
		log.Printf("restart after update failed: %v", err)
	}
}

// waitForPredecessor gives the old instance of an update restart time to drop
// its session file, which it does after closing its listener.
func waitForPredecessor(sessionPath string) {
	if os.Getenv(restartedEnv) != "1" {
		return
	}
	deadline := time.Now().Add(restartWaitLimit)
	for time.Now().Before(deadline) {
		if _, err := session.Read(sessionPath); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
