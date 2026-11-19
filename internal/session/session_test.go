package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteIsOwnerOnlyAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "session")
	secret, err := NewSecret()
	if err != nil || len(secret) != 64 {
		t.Fatalf("secret %q, %v", secret, err)
	}
	if err := Write(path, Info{Port: 8765, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, %v", info.Mode().Perm(), err)
		}
	}
	got, err := Read(path)
	if err != nil || got.Port != 8765 || got.Secret != secret {
		t.Fatalf("read %+v, %v", got, err)
	}
	if !strings.HasPrefix(got.LoginURL("n"), "http://127.0.0.1:8765/login?k=") {
		t.Fatalf("login url %q", got.LoginURL("n"))
	}
	if _, err := Read(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing file must be an error")
	}
}
