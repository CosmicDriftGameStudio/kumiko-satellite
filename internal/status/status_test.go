package status

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

type fakeSource struct {
	retries int
	snap    *syncer.Snapshot
}

func (f *fakeSource) Snapshot() syncer.Snapshot {
	if f.snap != nil {
		return *f.snap
	}
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
	h, _ := Handler(&fakeSource{}, "1.0", "https://app.example.com")
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
	h, _ := Handler(&fakeSource{}, "1.0", "https://app.example.com")
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
	h, _ := Handler(src, "1.0", "https://app.example.com")
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

func TestStatesQuotaRevokedChecking(t *testing.T) {
	get := func(snap syncer.Snapshot) string {
		h, _ := Handler(&fakeSource{snap: &snap}, "1.0", "https://app.example.com")
		return do(t, h, "GET", "127.0.0.1:8765", "/", nil, nil).Body.String()
	}
	q := get(syncer.Snapshot{State: syncer.StateQuota, LastError: "voll"})
	for _, want := range []string{"Pausiert: Speicher in der App voll", `class="card warn"`, "Admin", "Erneut versuchen"} {
		if !strings.Contains(q, want) {
			t.Errorf("quota page misses %q", want)
		}
	}
	r := get(syncer.Snapshot{State: syncer.StateRevoked})
	for _, want := range []string{"Gerät gesperrt", "kumiko-satellite pair --url https://app.example.com"} {
		if !strings.Contains(r, want) {
			t.Errorf("revoked page misses %q", want)
		}
	}
	if strings.Contains(r, "Erneut versuchen") || strings.Contains(r, "/retry") {
		t.Error("revoked page must hide the retry button")
	}
	c := get(syncer.Snapshot{State: syncer.StateChecking, Checking: 3})
	if !strings.Contains(c, "Prüfe neue Dateien") || strings.Contains(c, "Alles aktuell") || !strings.Contains(c, "Wird geprüft") {
		t.Error("checking page wrong")
	}
	if e := get(syncer.Snapshot{State: syncer.StateError}); !strings.Contains(e, ">Fehler<") || !strings.Contains(e, "Erneut versuchen") {
		t.Error("plain error stays red with retry")
	}
}

func TestRetryButtonOnlyWhereItHelps(t *testing.T) {
	cases := []struct {
		name string
		snap syncer.Snapshot
		want bool
	}{
		{"idle", syncer.Snapshot{State: syncer.StateIdle}, false},
		{"idle with failed", syncer.Snapshot{State: syncer.StateIdle, Failed: 1}, true},
		{"checking", syncer.Snapshot{State: syncer.StateChecking, Checking: 2}, false},
		{"syncing", syncer.Snapshot{State: syncer.StateSyncing}, false},
		{"error", syncer.Snapshot{State: syncer.StateError}, true},
		{"quota", syncer.Snapshot{State: syncer.StateQuota}, true},
		{"revoked", syncer.Snapshot{State: syncer.StateRevoked, Failed: 1}, false},
	}
	for _, c := range cases {
		snap := c.snap
		h, _ := Handler(&fakeSource{snap: &snap}, "1.0", "https://app.example.com")
		body := do(t, h, "GET", "127.0.0.1:8765", "/", nil, nil).Body.String()
		if got := strings.Contains(body, `action="/retry"`); got != c.want {
			t.Errorf("%s: retry button shown = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRevokedHidesProgressAndCounters(t *testing.T) {
	snap := syncer.Snapshot{State: syncer.StateRevoked, Queued: 1, Done: 0, Total: 1, Current: "a.pdf"}
	h, _ := Handler(&fakeSource{snap: &snap}, "1.0", "https://app.example.com")
	body := do(t, h, "GET", "127.0.0.1:8765", "/", nil, nil).Body.String()
	for _, bad := range []string{"<progress", "0 von 1", "Wartend", "Aktuelle Datei"} {
		if strings.Contains(body, bad) {
			t.Errorf("revoked page must not show %q", bad)
		}
	}
	if !strings.Contains(body, "kumiko-satellite pair --url") || !strings.Contains(body, "Protokoll") {
		t.Error("revoked page keeps hint and log")
	}
}
