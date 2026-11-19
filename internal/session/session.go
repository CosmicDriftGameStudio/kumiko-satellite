// Package session holds the per-user secret that proves a caller owns the
// running satellite. The file lives in the user's config dir, so other users of
// the same machine cannot read it and cannot use the local page.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
)

type Info struct {
	Port   int    `json:"port"`
	Secret string `json:"secret"`
	// PID is a hint for people looking at the file, never checked.
	PID int `json:"pid,omitempty"`
}

func Path() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "session"), nil
}

// NewSecret returns 32 random bytes as hex.
func NewSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Write stores the info atomically with owner-only rights.
func Write(path string, in Info) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Read(path string) (Info, error) {
	var in Info
	raw, err := os.ReadFile(path)
	if err != nil {
		return in, err
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, err
	}
	if in.Port < 1 || in.Port > 65535 || in.Secret == "" {
		return in, fmt.Errorf("session file %s is incomplete", path)
	}
	return in, nil
}

func (in Info) Addr() string { return fmt.Sprintf("127.0.0.1:%d", in.Port) }

// LoginURL is the link that sets the session cookie and redirects to the page;
// nonce is a single-use value, never the secret.
func (in Info) LoginURL(nonce string) string {
	return "http://" + in.Addr() + "/login?k=" + url.QueryEscape(nonce)
}
