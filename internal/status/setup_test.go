package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/session"
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
	loginDir string
	src      Source
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{settings: &fakeSettings{}, nonces: NewNonces(), loginDir: t.TempDir()}
	r.wiz = setup.New(context.Background(), func(config.Config) error { return nil }, func(string) error { return nil })
	h, err := newTestHandler(Options{
		Version: "1.0", Source: func() Source { return r.src }, Wizard: r.wiz, Settings: r.settings,
		AppURL: r.settings.AppURL, ShowWindow: func() { r.shown++ }, Secret: testSecret, Port: 8765, Nonces: r.nonces, LoginDir: r.loginDir,
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
	old := desktop.DialogAvailable
	t.Cleanup(func() { desktop.DialogAvailable = old })
	desktop.DialogAvailable = func() bool { return true } // CI runners have no folder dialog
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
	for _, target := range []string{"/setup/start", "/setup/cancel", "/setup/pick", "/setup/folder", "/setup/open", "/setup/repair", "/settings/folder"} {
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
	if err := SignalShow(addr, "wrong"); !errors.Is(err, ErrForeignServer) || r.shown != 0 {
		t.Fatalf("a wrong secret must not show the window: %v", err)
	}
	if err := SignalShow(addr, testSecret); err != nil || r.shown != 1 {
		t.Fatalf("signal: %v, shown = %d", err, r.shown)
	}

	other := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(other.Close)
	if IsSatellite(strings.TrimPrefix(other.URL, "http://")) {
		t.Fatal("a foreign server is not a satellite")
	}
	other.Close()
	if IsSatellite(strings.TrimPrefix(other.URL, "http://")) || SignalShow(strings.TrimPrefix(other.URL, "http://"), testSecret) == nil {
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
	if c.Name != "ksat_session_8765" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
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

// proof runs the client half of the handshake against h with the given secret.
func proof(t *testing.T, h http.Handler, secret string) (c2, m string) {
	t.Helper()
	c1 := strings.Repeat("ab", 32)
	w := doJSON(h, "/hello", map[string]string{"c1": c1})
	var out struct{ S, C2 string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("hello: %d %q", w.Code, w.Body.String())
	}
	if out.S != mac(testSecret, "server", c1) {
		t.Fatal("server answer must be the HMAC of the client challenge")
	}
	return out.C2, mac(secret, "client", out.C2)
}

func doJSON(h http.Handler, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandshakeRoutesRejectBadProofs(t *testing.T) {
	r := newRig(t)
	for _, path := range []string{"/login-nonce", "/show-window"} {
		if c := doJSON(r.h, path, map[string]string{"c2": "x", "m": "y"}).Code; c != 401 {
			t.Errorf("%s with made-up proof: %d", path, c)
		}
		c2, _ := proof(t, r.h, testSecret)
		if c := doJSON(r.h, path, map[string]string{"c2": c2, "m": mac("wrong", "client", c2)}).Code; c != 401 {
			t.Errorf("%s with wrong MAC: %d", path, c)
		}
		c2, m := proof(t, r.h, testSecret)
		if c := doJSON(r.h, path, map[string]string{"c2": c2, "m": m}).Code; c != 200 && c != 204 {
			t.Errorf("%s with right proof: %d", path, c)
		}
		if c := doJSON(r.h, path, map[string]string{"c2": c2, "m": m}).Code; c != 401 {
			t.Errorf("%s with reused c2: %d", path, c)
		}
	}
	if r.shown != 1 {
		t.Fatalf("window shown %d times", r.shown)
	}
}

func TestChallengeExpires(t *testing.T) {
	s, _ := newServer(Options{Secret: testSecret, Port: 8765})
	clock := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	s.challenges.Now = func() time.Time { return clock }
	c2, _ := s.challenges.New()
	clock = clock.Add(31 * time.Second)
	if s.challenges.Consume(c2) {
		t.Fatal("a challenge must expire after 30 s")
	}
}

func TestFetchNonceAndSignalOverTheHandshake(t *testing.T) {
	r := newRig(t)
	srv := httptest.NewServer(r.h)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	nonce, err := FetchNonce(addr, testSecret)
	if err != nil || nonce == "" || !r.nonces.Consume(nonce) {
		t.Fatalf("fetch: %q, %v", nonce, err)
	}
	if _, err := FetchNonce(addr, "wrong"); !errors.Is(err, ErrForeignServer) {
		t.Fatalf("wrong secret: %v", err)
	}
}

func TestForeignServerNeverGetsTheSecret(t *testing.T) {
	other, err := NewHandler(Options{Secret: "someone-elses", Port: 8765})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var paths, bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 2048)
		n, _ := r.Body.Read(buf)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, r.Header.Get("Authorization")+string(buf[:n]))
		mu.Unlock()
		other.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	if _, err := FetchNonce(addr, testSecret); !errors.Is(err, ErrForeignServer) {
		t.Fatalf("fetch: %v", err)
	}
	if err := SignalShow(addr, testSecret); !errors.Is(err, ErrForeignServer) {
		t.Fatalf("signal: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, p := range paths {
		if p != "/hello" || strings.Contains(bodies[i], testSecret) || strings.Contains(bodies[i], mac(testSecret, "client", "")) {
			t.Fatalf("request %d to %s must not happen or leak: %q", i, p, bodies[i])
		}
	}
	if len(paths) != 2 {
		t.Fatalf("paths %v", paths)
	}
}

func TestLoginFormAcceptsNullOriginOnly(t *testing.T) {
	r := newRig(t)
	nonce, _ := r.nonces.New()
	file, err := session.WriteLoginFile(r.loginDir, 8765, nonce)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"nonce": {nonce}, "f": {filepath.Base(file)}}
	evil := do(t, r.h, "POST", host, "/login", map[string]string{noCookie: "1", "Origin": "http://evil.example"}, form)
	if evil.Code != 403 || len(evil.Result().Cookies()) != 0 {
		t.Fatalf("foreign origin: %d", evil.Code)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("a refused login must not touch the file")
	}
	ok := do(t, r.h, "POST", host, "/login", map[string]string{noCookie: "1", "Origin": "null"}, form)
	cookies := ok.Result().Cookies()
	if ok.Code != 200 || len(cookies) != 1 || cookies[0].Name != "ksat_session_8765" || cookies[0].Value == testSecret {
		t.Fatalf("null origin login: %d %v", ok.Code, cookies)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("the login file must be gone after the login")
	}
	again := do(t, r.h, "POST", host, "/login", map[string]string{noCookie: "1", "Origin": "null"}, form)
	if again.Code != 401 {
		t.Fatalf("a nonce works once: %d", again.Code)
	}
	if w := do(t, r.h, "GET", host, "/login?k="+nonce, map[string]string{noCookie: "1", "Origin": "null"}, nil); w.Code != 401 {
		t.Fatalf("GET with a used nonce: %d", w.Code)
	}
}

func TestCookieOfAnotherPortIsNotAccepted(t *testing.T) {
	r := newRig(t)
	if c := do(t, r.h, "GET", "127.0.0.1:9999", "/", nil, nil).Code; c != 401 {
		t.Fatalf("right cookie on a foreign port: %d", c)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = host
	req.AddCookie(&http.Cookie{Name: "ksat_session_9999", Value: testCookie})
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("cookie named for another port: %d", w.Code)
	}
}

func TestSetupPagesFollowTheDesign(t *testing.T) {
	r := newRig(t)
	first := r.get(t, "/")
	for _, want := range []string{"1 Connect", "This computer delivers the files from one folder to your app.", "Version 1.0", ">Pair<"} {
		if !strings.Contains(first, want) {
			t.Errorf("step 1 misses %q", want)
		}
	}
	for _, gone := range []string{"Step 1 of 2", "Connect to your app", "refreshes every", "Get a new code", " disabled"} {
		if strings.Contains(first, gone) {
			t.Errorf("step 1 still has %q", gone)
		}
	}
	r.wiz.OpenFolder("https://app.example.com", "")
	second := r.get(t, "/")
	for _, want := range []string{"✓ Connect", "2 Folder", "Paired with app.example.com", "New and changed files in this folder are sent to the app."} {
		if !strings.Contains(second, want) {
			t.Errorf("step 2 misses %q", want)
		}
	}
	if strings.Contains(second, "Which folder should be uploaded?") || strings.Contains(second, "Step 2 of 2") {
		t.Error("step 2 keeps the old wording")
	}
}

func TestStatusPageFollowsTheDesign(t *testing.T) {
	r := newRig(t)
	at := time.Date(2026, 11, 19, 18, 42, 5, 0, time.Local)
	r.src = &fakeSource{snap: &syncer.Snapshot{State: syncer.StateIdle, Done: 3, Total: 3, LastSyncAt: at,
		Events: []syncer.Event{{Time: at, Message: "done"}}}}
	body := r.get(t, "/")
	for _, want := range []string{"Paired with app.example.com · /data", "3 of 3 files delivered", `<progress class="ok"`, "Nov 19, 2026, 6:42 PM",
		"6:42:05 PM", "<details>", "<summary>Log</summary>", `href="/settings"`, ">Failed<", "Version 1.0", ">●<"} {
		if !strings.Contains(body, want) {
			t.Errorf("status page misses %q", want)
		}
	}
	if strings.Contains(body, ">Waiting<") || strings.Contains(body, "refreshes every") || strings.Contains(body, "Log (last 200 events)") {
		t.Error("status page keeps old elements")
	}
	r.src = &fakeSource{snap: &syncer.Snapshot{State: syncer.StateRevoked, LastError: "Device revoked, pair again"}}
	if rev := r.get(t, "/"); strings.Contains(rev, "Last error") {
		t.Error("a revoked device must not repeat its reason as Last error")
	}
}

func TestDeniedPageUsesTheSameLayout(t *testing.T) {
	r := newRig(t)
	w := do(t, r.h, "GET", host, "/", map[string]string{noCookie: "1"}, nil)
	body := w.Body.String()
	if w.Code != 401 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(body, "IBM Plex Sans") || !strings.Contains(body, "Kumiko Satellite") || !strings.Contains(body, "Open Kumiko Satellite from its icon.") {
		t.Fatalf("denied page: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestLiveUpdateScriptIsTiedToTheResponseNonce(t *testing.T) {
	r := newRig(t)
	r.src = &fakeSource{}
	w := do(t, r.h, "GET", host, "/", nil, nil)
	csp, body := w.Header().Get("Content-Security-Policy"), w.Body.String()
	i := strings.Index(csp, "script-src 'nonce-")
	if i < 0 || strings.Contains(csp, "script-src 'unsafe-inline'") || strings.Contains(csp, "'unsafe-eval'") {
		t.Fatalf("csp %q", csp)
	}
	nonce := strings.SplitN(csp[i+len("script-src 'nonce-"):], "'", 2)[0]
	if !strings.Contains(body, `<script nonce="`+nonce+`">`) || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatal("the script must carry this response's nonce")
	}
	if other := do(t, r.h, "GET", host, "/", nil, nil).Header().Get("Content-Security-Policy"); other == csp {
		t.Fatal("every response needs its own nonce")
	}
	for _, want := range []string{`data-live="status"`, `data-live="log"`, "<details>", `data-page="status"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Contains(body, "http-equiv") || !strings.Contains(body, "<noscript>") || !strings.Contains(body, "Refresh") {
		t.Error("no meta refresh; a Refresh link for browsers without scripts")
	}
}

func TestCancelFreesTheAddressFieldAgain(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "device_authorization") {
			_, _ = w.Write([]byte(`{"device_code":"d","user_code":"AB12-CD34","verification_uri":"https://app/pair","expires_in":600,"interval":5}`))
			return
		}
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	t.Cleanup(app.Close)
	r := newRig(t)
	r.wiz.Submit(app.URL)
	waiting := r.get(t, "/")
	for _, want := range []string{"AB12-CD34", `action="/setup/cancel"`, ">Cancel<", " disabled", `data-page="waiting"`, `data-live="wait"`, `<script nonce=`} {
		if !strings.Contains(waiting, want) {
			t.Errorf("waiting page misses %q", want)
		}
	}
	if c := r.post(t, "/setup/cancel", url.Values{}).Code; c != 303 {
		t.Fatalf("cancel: %d", c)
	}
	form := r.get(t, "/")
	if strings.Contains(form, "AB12-CD34") || strings.Contains(form, " disabled") || !strings.Contains(form, `data-page="form"`) || !strings.Contains(form, app.URL) {
		t.Error("after cancel the code is gone, Pair is active and the address stays")
	}
}
