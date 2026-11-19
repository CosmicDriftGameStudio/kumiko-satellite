// Package api is the HTTP client for the connectors endpoints of a Kumiko app.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ClientID = "kumiko-satellite"
	// The guide names /connectors as the default mount point of the routes.
	mountPath = "/connectors"
)

// Endpoint turns the app URL into the connectors base URL. Plain http is only
// accepted for loopback so a token never travels unencrypted by accident.
func Endpoint(appURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(appURL))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid app URL %q", appURL)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("app URL must not contain credentials, query or fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return "", errors.New("app URL must use https (http is only allowed for localhost)")
		}
	default:
		return "", errors.New("app URL must use https")
	}
	return strings.TrimRight(u.String(), "/") + mountPath, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Error is a non-success answer of the server.
type Error struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("server answered %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("server answered %d", e.Status)
}

// Quota reports that the tenant has no storage left; retrying does not help.
func (e *Error) Quota() bool {
	return e.Status == http.StatusInsufficientStorage || e.Code == "storage_quota_exceeded"
}

// Revoked reports that the token is no longer valid; the device must pair again.
func (e *Error) Revoked() bool {
	return e.Status == http.StatusUnauthorized || e.Code == "connector_revoked"
}

// Rejected reports a refusal of this very file that no retry will change.
func (e *Error) Rejected() bool {
	return e.Status == http.StatusUnprocessableEntity ||
		(e.Status == http.StatusRequestEntityTooLarge && !e.Quota())
}

func ErrorFromResponse(resp *http.Response) *Error {
	e := &Error{Status: resp.StatusCode}
	var body struct {
		Error string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if json.Unmarshal(raw, &body) == nil {
		e.Code = body.Error
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		e.RetryAfter = time.Duration(secs) * time.Second
	}
	return e
}

// Client talks to an app with a device token.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(appURL, token string) (*Client, error) {
	base, err := Endpoint(appURL)
	if err != nil {
		return nil, err
	}
	// No overall timeout cap below 15 minutes: a 50 MiB file may travel a slow line.
	return &Client{base: base, token: token, http: &http.Client{Timeout: 15 * time.Minute}}, nil
}

type Upload struct {
	Path        string // forward slashes, relative to the watched folder
	SHA256      string // lower-case hex
	Mtime       time.Time
	ContentType string
	Size        int64
	Body        io.Reader
}

func (c *Client) Upload(ctx context.Context, u Upload) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/files", u.Body)
	if err != nil {
		return err
	}
	req.ContentLength = u.Size
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Kumiko-Path", encodePath(u.Path))
	req.Header.Set("X-Kumiko-Sha256", u.SHA256)
	req.Header.Set("X-Kumiko-Mtime", u.Mtime.UTC().Format(time.RFC3339))
	if u.ContentType == "" {
		u.ContentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", u.ContentType)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}
	return ErrorFromResponse(resp)
}

func encodePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// SyncInfo is the optional sync block of the heartbeat.
type SyncInfo struct {
	State      string `json:"state"`
	Queued     int    `json:"queued"`
	Failed     int    `json:"failed"`
	LastError  string `json:"lastError,omitempty"`
	LastSyncAt string `json:"lastSyncAt,omitempty"`
}

// Heartbeat reports version and sync state; retry is true when the server asks
// for failed files to be tried again.
func (c *Client) Heartbeat(ctx context.Context, version string, sync *SyncInfo) (retry bool, err error) {
	if version == "" {
		version = "dev"
	}
	if len(version) > 64 {
		version = version[:64]
	}
	payload, err := json.Marshal(struct {
		Version string    `json:"version"`
		Sync    *SyncInfo `json:"sync,omitempty"`
	}{version, sync})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/heartbeat", strings.NewReader(string(payload)))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, ErrorFromResponse(resp)
	}
	var out struct {
		Retry bool `json:"retry"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out)
	return out.Retry, nil
}
