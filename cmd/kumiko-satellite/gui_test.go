package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/session"
)

func TestGuiStopsOnCancelAndRemovesSessionFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	path, err := session.Path()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- gui(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := session.Read(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session file never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("gui returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gui did not return within 5 s of the cancel")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the session file must be gone after a clean stop")
	}
}
