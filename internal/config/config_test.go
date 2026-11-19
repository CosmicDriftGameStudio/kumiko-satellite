package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveIsOwnerOnlyAndRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	want := Config{URL: "https://app.example.com", Token: "ksat_x", Folder: "/srv/belege"}
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
	got, err := Load(p)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
}
