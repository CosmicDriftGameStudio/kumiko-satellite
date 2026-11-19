// Package pairing implements the OAuth device flow (RFC 8628) against a Kumiko app.
package pairing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/api"
)

const (
	grantType    = "urn:ietf:params:oauth:grant-type:device_code"
	slowDownStep = 5 * time.Second
	minInterval  = time.Second
)

var ErrExpired = errors.New("der Kopplungscode ist abgelaufen, bitte neu starten")

type Authorization struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type Flow struct {
	base  string
	http  *http.Client
	Sleep func(ctx context.Context, d time.Duration) error // replaceable in tests
}

func New(appURL string) (*Flow, error) {
	base, err := api.Endpoint(appURL)
	if err != nil {
		return nil, err
	}
	return &Flow{base: base, http: &http.Client{Timeout: 30 * time.Second}, Sleep: sleep}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (f *Flow) post(ctx context.Context, path string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.http.Do(req)
}

func (f *Flow) Start(ctx context.Context) (Authorization, error) {
	var a Authorization
	resp, err := f.post(ctx, "/v1/device_authorization", url.Values{"client_id": {api.ClientID}})
	if err != nil {
		return a, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return a, api.ErrorFromResponse(resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&a); err != nil {
		return a, fmt.Errorf("unreadable pairing answer: %w", err)
	}
	if a.DeviceCode == "" || a.UserCode == "" {
		return a, errors.New("pairing answer misses device or user code")
	}
	return a, nil
}

// Poll waits for the admin to confirm and returns the access token.
func (f *Flow) Poll(ctx context.Context, a Authorization) (string, error) {
	interval := time.Duration(a.Interval) * time.Second
	if interval < minInterval {
		interval = 5 * time.Second
	}
	if a.ExpiresIn > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(a.ExpiresIn)*time.Second)
		defer cancel()
	}
	for {
		if err := f.Sleep(ctx, interval); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return "", ErrExpired
			}
			return "", err
		}
		token, code, err := f.pollOnce(ctx, a.DeviceCode)
		if err != nil {
			return "", err
		}
		switch code {
		case "":
			return token, nil
		case "authorization_pending":
		case "slow_down":
			interval += slowDownStep
		case "expired_token":
			return "", ErrExpired
		case "access_denied":
			return "", errors.New("die Kopplung wurde in der App abgelehnt")
		default:
			return "", fmt.Errorf("Kopplung fehlgeschlagen: %s", code)
		}
	}
}

func (f *Flow) pollOnce(ctx context.Context, deviceCode string) (token, code string, err error) {
	resp, err := f.post(ctx, "/v1/token", url.Values{
		"grant_type":  {grantType},
		"device_code": {deviceCode},
		"client_id":   {api.ClientID},
	})
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var ok struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&ok); err != nil || ok.AccessToken == "" {
			return "", "", errors.New("unreadable token answer")
		}
		return ok.AccessToken, "", nil
	}
	e := api.ErrorFromResponse(resp)
	if e.Code == "" {
		return "", "", e
	}
	return "", e.Code, nil
}
