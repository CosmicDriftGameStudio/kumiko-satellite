package status

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/setup"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

type fakeSettings struct{ autostart bool }

func (f *fakeSettings) AppURL() string             { return "https://app.example.com" }
func (f *fakeSettings) Folder() string             { return "/data" }
func (f *fakeSettings) SetFolder(string) error     { return nil }
func (f *fakeSettings) Autostart() bool            { return f.autostart }
func (f *fakeSettings) SetAutostart(on bool) error { f.autostart = on; return nil }

type rig struct {
	h        http.Handler
	wiz      *setup.Wizard
	settings *fakeSettings
	shown    int
	nonces   *Nonces
	src      Source
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{settings: &fakeSettings{}, nonces: NewNonces()}
	r.wiz = setup.New(context.Background(), func(config.Config) error { return nil }, func(string) error { return nil })
	h, err := newTestHandler(Options{
		Version: "1.0", Source: func() Source { return r.src }, Wizard: r.wiz, Settings: r.settings,
		AppURL: r.settings.AppURL, ShowWindow: func() { r.shown++ }, Secret: testSecret, Nonces: r.nonces,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.h = h
	return r
}

const host = "127.0.0.1:8765"

func (r *rig) get(t *testing.T, target string) string {
	t.Helper()
	return do(t, r.h, "GET", host, target, nil, nil).Body.String()
}

func (r *rig) token(t *testing.T) string {
	body := r.get(t, "/")
	i := strings.Index(body, `name="token" value="`)
	if i < 0 {
		t.Fatal("page has no token")
	}
	i += len(`name="token" value="`)
	return body[i : i+strings.Index(body[i:], `"`)]
}

func (r *rig) post(t *testing.T, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("token", r.token(t))
	return do(t, r.h, "POST", host, target, map[string]string{"Origin": "http://" + host}, form)
}

func TestSetupStepsAreOrderedAddressFirstThenFolder(t *testing.T) {
	r := newRig(t)
	first := r.get(t, "/")
	if !strings.Contains(first, "App address") || !strings.Contains(first, "Pair") || strings.Contains(first, `name="folder"`) {
		t.Fatal("step 1 shows the address only")
	}
	r.wiz.OpenFolder("https://app.example.com", "")
	second := r.get(t, "/")
	for _, want := range []string{`name="folder"`, "Browse", "Start automatically when I sign in", "Done"} {
		if !strings.Contains(second, want) {
			t.Errorf("step 2 misses %q", want)
		}
	}
	if strings.Contains(second, "App address") {
		t.Error("step 2 must not ask for the address again")
	}
}

func TestSetupPostsNeedTokenAndOrigin(t *testing.T) {
	r := newRig(t)
	for _, target := range []string{"/setup/start", "/setup/pick", "/setup/folder", "/setup/open", "/setup/repair", "/settings/folder"} {
		if c := do(t, r.h, "POST", host, target, map[string]string{"Origin": "http://" + host}, url.Values{}).Code; c != 403 {
			t.Errorf("%s without token: %d", target, c)
		}
		form := url.Values{"token": {r.token(t)}}
		if c := do(t, r.h, "POST", host, target, map[string]string{"Origin": "http://evil.example.com"}, form).Code; c != 403 {
			t.Errorf("%s with foreign origin: %d", target, c)
		}
		if c := do(t, r.h, "POST", host, target, nil, form).Code; c != 403 {
			t.Errorf("%s without origin: %d", target, c)
		}
	}
	if r.shown != 0 {
		t.Fatal("rejected requests must not show the window")
	}
}

func TestBadAddressAndFolderShowGermanFieldErrors(t *testing.T) {
	r := newRig(t)
	r.post(t, "/setup/start", url.Values{"url": {"kein url"}})
	if body := r.get(t, "/"); !strings.Contains(body, "Please enter the address of your app") {
		t.Error("address error missing")
	}
	r.wiz.OpenFolder("https://app.example.com", "")
	r.post(t, "/setup/folder", url.Values{"folder": {"/"}})
	if body := r.get(t, "/"); !strings.Contains(body, "not a whole drive") {
		t.Error("folder guard error missing")
	}
}

func TestPickFolderFillsTheField(t *testing.T) {
	old := desktop.PickFolder
	t.Cleanup(func() { desktop.PickFolder = old })
	chosen := t.TempDir()
	desktop.PickFolder = func(context.Context) (string, error) { return chosen, nil }
	r := newRig(t)
	r.wiz.OpenFolder("https://app.example.com", "")
	r.post(t, "/setup/pick", url.Values{"folder": {""}, "autostart": {"on"}})
	if body := r.get(t, "/"); !strings.Contains(body, chosen) {
		t.Error("picked folder not shown in the field")
	}
}

func TestRevokedOffersRepairButtonInsteadOfCommand(t *testing.T) {
	r := newRig(t)
	r.src = &fakeSource{snap: &syncer.Snapshot{State: syncer.StateRevoked}}
	body := r.get(t, "/")
	if !strings.Contains(body, `action="/setup/repair"`) || !strings.Contains(body, "Pair again") || strings.Contains(body, "kumiko-satellite pair") {
		t.Error("revoked page must offer the Neu koppeln button, not the command")
	}
}

func TestSecondInstanceSignalsRunningOne(t *testing.T) {
	r := newRig(t)
	srv := httptest.NewServer(r.h)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	if !IsSatellite(addr) {
		t.Fatal("running satellite not recognised")
	}
	if SignalShow(addr, "wrong") || r.shown != 0 {
		t.Fatal("a wrong secret must not show the window")
	}
	if !SignalShow(addr, testSecret) || r.shown != 1 {
		t.Fatalf("signal: shown = %d", r.shown)
	}

	other := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(other.Close)
	if IsSatellite(strings.TrimPrefix(other.URL, "http://")) {
		t.Fatal("a foreign server is not a satellite")
	}
	other.Close()
	if IsSatellite(strings.TrimPrefix(other.URL, "http://")) || SignalShow(strings.TrimPrefix(other.URL, "http://"), testSecret) {
		t.Fatal("closed port must not count as instance")
	}
}

func TestRoutesNeedTheSessionCookie(t *testing.T) {
	r := newRig(t)
	r.src = &fakeSource{}
	nocookie := map[string]string{noCookie: "1", "Origin": "http://" + host}
	for _, c := range []struct{ method, target string }{
		{"GET", "/"}, {"GET", "/settings"}, {"POST", "/setup/start"}, {"POST", "/settings/folder"}, {"POST", "/retry"},
	} {
		w := do(t, r.h, c.method, host, c.target, nocookie, url.Values{"token": {"x"}, "url": {"https://a.example.com"}, "folder": {"/tmp"}})
		body := w.Body.String()
		if w.Code != 401 || strings.Contains(body, "a.pdf") || strings.Contains(body, "token") || strings.Contains(body, "Version") {
			t.Errorf("%s %s without cookie: %d %q", c.method, c.target, w.Code, body)
		}
	}
	if w := do(t, r.h, "GET", host, "/fonts/ibm-plex-sans-latin-400.woff2", nocookie, nil); w.Code != 200 {
		t.Errorf("fonts stay public: %d", w.Code)
	}
}

func TestLoginSetsStrictHttpOnlyCookie(t *testing.T) {
	r := newRig(t)
	for _, k := range []string{"wrong", testSecret, ""} {
		bad := do(t, r.h, "GET", host, "/login?k="+k, map[string]string{noCookie: "1"}, nil)
		if bad.Code != 401 || len(bad.Result().Cookies()) != 0 {
			t.Fatalf("k=%q: %d, cookies %v", k, bad.Code, bad.Result().Cookies())
		}
	}
	nonce, err := r.nonces.New()
	if err != nil {
		t.Fatal(err)
	}
	ok := do(t, r.h, "GET", host, "/login?k="+nonce, map[string]string{noCookie: "1"}, nil)
	cookies := ok.Result().Cookies()
	if ok.Code != 303 || ok.Header().Get("Location") != "/" || len(cookies) != 1 {
		t.Fatalf("login: %d %q %v", ok.Code, ok.Header().Get("Location"), cookies)
	}
	c := cookies[0]
	if c.Value == testSecret || len(c.Value) != 64 {
		t.Fatalf("cookie must be a random session ID, not the secret: %q", c.Value)
	}
	if c.Name != "ksat_session" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie %+v", c)
	}
}

func TestNonceWorksOnceAndExpires(t *testing.T) {
	r := newRig(t)
	clock := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	r.nonces.Now = func() time.Time { return clock }
	login := func(n string) int {
		return do(t, r.h, "GET", host, "/login?k="+n, map[string]string{noCookie: "1"}, nil).Code
	}
	once, _ := r.nonces.New()
	if login(once) != 303 || login(once) != 401 {
		t.Fatal("a nonce must work exactly once")
	}
	old, _ := r.nonces.New()
	clock = clock.Add(61 * time.Second)
	if login(old) != 401 {
		t.Fatal("an expired nonce must be refused")
	}
	var open []string
	for range 10 {
		clock = clock.Add(time.Second)
		n, _ := r.nonces.New()
		open = append(open, n)
	}
	if login(open[0]) != 401 || login(open[9]) != 303 {
		t.Fatal("at most 8 nonces stay open, the oldest is dropped")
	}
}

func TestLoginNonceNeedsBearer(t *testing.T) {
	r := newRig(t)
	for _, hdr := range []map[string]string{{noCookie: "1"}, {noCookie: "1", "Authorization": "Bearer wrong"}, {"Authorization": "Bearer wrong"}} {
		if c := do(t, r.h, "POST", host, "/login-nonce", hdr, nil).Code; c != 401 {
			t.Errorf("%v: %d", hdr, c)
		}
	}
	srv := httptest.NewServer(r.h)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	nonce, err := FetchNonce(addr, testSecret)
	if err != nil || nonce == "" {
		t.Fatalf("fetch: %q, %v", nonce, err)
	}
	if _, err := FetchNonce(addr, "wrong"); err == nil {
		t.Fatal("wrong secret must fail")
	}
	if !r.nonces.Consume(nonce) {
		t.Fatal("the fetched nonce must be valid")
	}
}

func TestShowWindowNeedsBearer(t *testing.T) {
	r := newRig(t)
	for _, hdr := range []map[string]string{
		{noCookie: "1"},
		{noCookie: "1", "Authorization": "Bearer wrong"},
		{"Authorization": "Bearer wrong"},
	} {
		if c := do(t, r.h, "POST", host, "/show-window", hdr, nil).Code; c != 401 {
			t.Errorf("%v: %d", hdr, c)
		}
	}
	if r.shown != 0 {
		t.Fatal("window shown without bearer")
	}
	if c := do(t, r.h, "POST", host, "/show-window", map[string]string{noCookie: "1", "Authorization": "Bearer " + testSecret}, nil).Code; c != 204 || r.shown != 1 {
		t.Fatalf("with bearer: %d shown %d", c, r.shown)
	}
}
