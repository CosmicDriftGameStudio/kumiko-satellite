package status

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

type fakeSource struct{ retries int }

func (f *fakeSource) Snapshot() syncer.Snapshot {
	return syncer.Snapshot{State: syncer.StateError, Done: 12, Total: 340, Current: "a.pdf", LastError: "<b>boom</b>",
		Events: []syncer.Event{{Message: "neu"}, {Message: "alt"}}}
}
func (f *fakeSource) Retry() { f.retries++ }

func do(t *testing.T, h http.Handler, method, host, target string, hdr map[string]string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, body)
	r.Host = host
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHostCheck(t *testing.T) {
	h, _ := Handler(&fakeSource{}, "1.0")
	for host, want := range map[string]int{
		"127.0.0.1:8765": 200, "localhost:8765": 200,
		"evil.example.com": 403, "evil.example.com:8765": 403, "127.0.0.1": 403, "127.0.0.1.evil.com:80": 403,
	} {
		if got := do(t, h, "GET", host, "/", nil, nil).Code; got != want {
			t.Errorf("host %q: got %d want %d", host, got, want)
		}
	}
}

func TestPageContent(t *testing.T) {
	h, _ := Handler(&fakeSource{}, "1.0")
	body := do(t, h, "GET", "127.0.0.1:8765", "/", nil, nil).Body.String()
	for _, want := range []string{"12 von 340", "a.pdf", "Erneut versuchen", "&lt;b&gt;boom&lt;/b&gt;", `http-equiv="refresh"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Index(body, "neu") > strings.Index(body, "alt") {
		t.Error("newest event must come first")
	}
}

func TestRetryNeedsTokenAndOrigin(t *testing.T) {
	src := &fakeSource{}
	h, _ := Handler(src, "1.0")
	host := "127.0.0.1:8765"
	origin := map[string]string{"Origin": "http://" + host}

	if c := do(t, h, "POST", host, "/retry", origin, url.Values{}).Code; c != 403 {
		t.Errorf("POST without token: %d", c)
	}
	if c := do(t, h, "POST", host, "/retry", origin, url.Values{"token": {"wrong"}}).Code; c != 403 {
		t.Errorf("POST with wrong token: %d", c)
	}
	page := do(t, h, "GET", host, "/", nil, nil).Body.String()
	i := strings.Index(page, `name="token" value="`) + len(`name="token" value="`)
	token := page[i : i+strings.Index(page[i:], `"`)]
	form := url.Values{"token": {token}}

	if c := do(t, h, "POST", host, "/retry", nil, form).Code; c != 403 {
		t.Errorf("POST without Origin: %d", c)
	}
	if c := do(t, h, "POST", host, "/retry", map[string]string{"Origin": "http://evil.example.com"}, form).Code; c != 403 {
		t.Errorf("POST with foreign Origin: %d", c)
	}
	if src.retries != 0 {
		t.Fatal("rejected requests must not retry")
	}
	if c := do(t, h, "POST", host, "/retry", origin, form).Code; c != 303 || src.retries != 1 {
		t.Errorf("valid POST: code %d retries %d", c, src.retries)
	}
	if c := do(t, h, "POST", "evil.example.com", "/retry", map[string]string{"Origin": "http://evil.example.com"}, form).Code; c != 403 {
		t.Errorf("POST with foreign host: %d", c)
	}
}

func TestValidateAddr(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:8765": true, "localhost:1": true, "[::1]:80": true, "0.0.0.0:8765": false, ":8765": false, "192.168.1.5:80": false} {
		if (ValidateAddr(addr) == nil) != ok {
			t.Errorf("%s: want ok=%v", addr, ok)
		}
	}
}
