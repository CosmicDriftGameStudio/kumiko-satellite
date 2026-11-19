package filter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsBallast(t *testing.T) {
	for _, name := range []string{"$RECYCLE.BIN", "System Volume Information", "Thumbs.db", "DESKTOP.INI", ".DS_Store",
		"pagefile.sys", "Hiberfil.sys", "swapfile.sys", "~$Budget.xlsx"} {
		if !IsBallast(name) {
			t.Errorf("%q should be ballast", name)
		}
	}
	for _, name := range []string{"report.pdf", "a~$b.txt", "thumbs.db.pdf"} {
		if IsBallast(name) {
			t.Errorf("%q should not be ballast", name)
		}
	}
}

func TestSkip(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) os.FileInfo {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	if !Skip("Thumbs.db", write("Thumbs.db")) {
		t.Error("ballast must be skipped")
	}
	if Skip("invoice.pdf", write("invoice.pdf")) {
		t.Error("normal file must pass")
	}
	if os.PathSeparator == '/' && !Skip(".secret", write(".secret")) {
		t.Error("dotfile must be skipped outside Windows")
	}
}
