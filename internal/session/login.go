package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A login file lets the browser post the nonce to the page without the nonce
// appearing in any command line, which other users can read on Linux. It lives
// in the owner-only config dir.

const LoginFileTTL = 60 * time.Second

var loginFileName = regexp.MustCompile(`^login-[0-9a-f]{32}\.html$`)

// WriteLoginFile writes the auto-submitting page into dir and returns its path.
func WriteLoginFile(dir string, port int, nonce string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "login-" + hex.EncodeToString(raw) + ".html"
	path := filepath.Join(dir, name)
	page := fmt.Sprintf(`<!doctype html><meta charset="utf-8"><title>Kumiko Satellite</title>
<form method="post" action="http://127.0.0.1:%d/login">
<input type="hidden" name="nonce" value="%s"><input type="hidden" name="f" value="%s">
<noscript><button>Open Kumiko Satellite</button></noscript></form>
<script>document.forms[0].submit()</script>
`, port, nonce, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(page); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	return path, f.Close()
}

// FileURL turns a local path into a file:// URL.
func FileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// RemoveLoginFile deletes a login file by name; anything else is ignored.
func RemoveLoginFile(dir, name string) {
	if dir != "" && loginFileName.MatchString(name) {
		os.Remove(filepath.Join(dir, name))
	}
}

// SweepLoginFiles deletes login files older than LoginFileTTL.
func SweepLoginFiles(dir string, now time.Time) {
	if dir == "" {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && loginFileName.MatchString(e.Name()) && now.Sub(info.ModTime()) >= LoginFileTTL {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Remove deletes the session file on clean shutdown.
func Remove(path string) { os.Remove(path) }
