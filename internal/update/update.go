// Package update looks for a newer GitHub release and swaps the running
// executable for it after checking the SHA-256 the release publishes.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	LatestURL      = "https://api.github.com/repos/CosmicDriftGameStudio/kumiko-satellite/releases/latest"
	DownloadPrefix = "https://github.com/CosmicDriftGameStudio/kumiko-satellite/releases/download/"
	maxBinaryBytes = 64 << 20
	maxJSONBytes   = 1 << 20
)

// Release is the newer version with what is needed to install it.
type Release struct {
	Version  string // without leading v
	PageURL  string
	AssetURL string // empty on macOS: the app bundle is installed by hand
	SHA256   string // hex
}

// Checker talks to the release API. The zero value uses the fixed GitHub
// URLs and the running platform; the fields exist for tests.
type Checker struct {
	BaseURL        string
	DownloadPrefix string
	Client         *http.Client
	GOOS, GOARCH   string
}

func (c Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (c Checker) target() (string, string) {
	goos, goarch := c.GOOS, c.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// AssetName is the release asset of a platform (macOS ships an .app.zip instead).
func AssetName(goos, goarch string) string {
	name := "kumiko-satellite-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

type apiRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// Check reports the latest release and whether it is newer than current.
// "dev" and unparsable versions never update.
func (c Checker) Check(ctx context.Context, current string) (Release, bool, error) {
	cur, ok := parseVersion(current)
	if !ok {
		return Release{}, false, nil
	}
	base := c.BaseURL
	if base == "" {
		base = LatestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return Release{}, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client().Do(req)
	if err != nil {
		return Release{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, false, fmt.Errorf("release check: HTTP %d", resp.StatusCode)
	}
	var api apiRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(&api); err != nil {
		return Release{}, false, fmt.Errorf("release check: %w", err)
	}
	latest, ok := parseVersion(api.TagName)
	if !ok || !newer(latest, cur) {
		return Release{}, false, nil
	}
	rel := Release{Version: strings.TrimPrefix(api.TagName, "v"), PageURL: api.HTMLURL}
	goos, goarch := c.target()
	if goos == "darwin" {
		return rel, true, nil
	}
	want := AssetName(goos, goarch)
	prefix := c.DownloadPrefix
	if prefix == "" {
		prefix = DownloadPrefix
	}
	for _, a := range api.Assets {
		if a.Name != want {
			continue
		}
		if !strings.HasPrefix(a.URL, prefix) {
			return Release{}, false, fmt.Errorf("release asset %s has an unexpected download URL", a.Name)
		}
		hexSum, ok := strings.CutPrefix(a.Digest, "sha256:")
		if raw, err := hex.DecodeString(hexSum); !ok || err != nil || len(raw) != sha256.Size {
			return Release{}, false, fmt.Errorf("release asset %s has no sha256 digest", a.Name)
		}
		rel.AssetURL, rel.SHA256 = a.URL, strings.ToLower(hexSum)
		return rel, true, nil
	}
	return Release{}, false, fmt.Errorf("release %s has no asset %s", rel.Version, want)
}

type version [3]int

func parseVersion(s string) (version, bool) {
	var v version
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func newer(a, b version) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// Apply downloads the asset next to exePath, checks its digest and swaps it in;
// the old file stays as <exe>.old until CleanupOld.
func (c Checker) Apply(ctx context.Context, rel Release, exePath string) error {
	if rel.AssetURL == "" {
		return errors.New("this release has no installable file for this platform")
	}
	prefix := c.DownloadPrefix
	if prefix == "" {
		prefix = DownloadPrefix
	}
	if !strings.HasPrefix(rel.AssetURL, prefix) {
		return errors.New("unexpected download URL")
	}
	newPath, oldPath := exePath+".new", exePath+".old"
	if err := c.download(ctx, rel, newPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	goos, _ := c.target()
	if goos != "windows" {
		if err := os.Chmod(newPath, 0o755); err != nil {
			_ = os.Remove(newPath)
			return err
		}
	}
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath)
		_ = os.Remove(newPath)
		return err
	}
	return nil
}

func (c Checker) download(ctx context.Context, rel Release, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.AssetURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBinaryBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxBinaryBytes {
		return errors.New("download is larger than the allowed size")
	}
	if hex.EncodeToString(h.Sum(nil)) != rel.SHA256 {
		return errors.New("download does not match the published checksum")
	}
	return nil
}

// CleanupOld removes the file left by the previous update; on Windows it may
// still be locked, then the next start removes it.
func CleanupOld(exePath string) {
	_ = os.Remove(exePath + ".old")
}
