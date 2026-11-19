package status

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SignalShow asks the running instance to open its window again.
func SignalShow(addr string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/instance", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(get)
	if err != nil {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		return false
	}
	post, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/show-window",
		strings.NewReader(url.Values{"token": {string(raw)}}.Encode()))
	if err != nil {
		return false
	}
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://"+addr)
	resp, err = http.DefaultClient.Do(post)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
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
