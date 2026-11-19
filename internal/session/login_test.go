package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoginFileIsOwnerOnlyAndRemovable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	path, err := WriteLoginFile(dir, 8765, "the-nonce")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, %v", info.Mode().Perm(), err)
		}
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", info.Mode().Perm())
		}
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "http://127.0.0.1:8765/login") || !strings.Contains(string(raw), "the-nonce") {
		t.Fatalf("page %q", raw)
	}
	if u := FileURL(path); !strings.HasPrefix(u, "file:///") || strings.Contains(u, "the-nonce") {
		t.Fatalf("url %q", u)
	}
	RemoveLoginFile(dir, "../session")
	RemoveLoginFile(dir, filepath.Base(path))
	if _, err := os.Stat(path); err == nil {
		t.Fatal("login file must be removed")
	}
}

func TestSweepRemovesOnlyOldLoginFiles(t *testing.T) {
	dir := t.TempDir()
	fresh, _ := WriteLoginFile(dir, 1, "a")
	old, _ := WriteLoginFile(dir, 1, "b")
	other := filepath.Join(dir, "session")
	_ = os.WriteFile(other, []byte("x"), 0o600)
	past := time.Now().Add(-2 * LoginFileTTL)
	_ = os.Chtimes(old, past, past)
	_ = os.Chtimes(other, past, past)
	SweepLoginFiles(dir, time.Now())
	for p, want := range map[string]bool{fresh: true, old: false, other: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}
