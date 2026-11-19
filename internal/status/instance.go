package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SignalShow asks the running instance to open its window again; only the
// owner of the session secret can.
func SignalShow(addr, secret string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/show-window", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

// FetchNonce asks the running instance for a login nonce; only the owner of the secret can.
func FetchNonce(addr, secret string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/login-nonce", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("instance refused the request: %s", resp.Status)
	}
	var out struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&out); err != nil || out.Nonce == "" {
		return "", errors.New("unreadable nonce answer")
	}
	return out.Nonce, nil
}

const instanceHeader = "X-Kumiko-Satellite"

// IsSatellite reports whether a satellite already answers on addr, so a second
// start only has to open the browser.
func IsSatellite(addr string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.Header.Get(instanceHeader) == "1"
}
