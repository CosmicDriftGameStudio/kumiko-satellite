package main

import (
	"os"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/session"
)

// loginFileURL writes the login page for nonce and returns the file:// URL to
// open; the page is removed when the nonce is used, or after a minute.
func loginFileURL(dir string, port int, nonce string) (string, error) {
	path, err := session.WriteLoginFile(dir, port, nonce)
	if err != nil {
		return "", err
	}
	time.AfterFunc(session.LoginFileTTL, func() { os.Remove(path) })
	return session.FileURL(path), nil
}
