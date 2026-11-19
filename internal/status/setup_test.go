package status

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
	src      Source
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{settings: &fakeSettings{}}
	r.wiz = setup.New(context.Background(), func(config.Config) error { return nil }, func(string) error { return nil })
	h, err := NewHandler(Options{
		Version: "1.0", Source: func() Source { return r.src }, Wizard: r.wiz, Settings: r.settings,
		AppURL: r.settings.AppURL, ShowWindow: func() { r.shown++ },
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
	for _, target := range []string{"/setup/start", "/setup/pick", "/setup/folder", "/setup/open", "/setup/repair", "/settings/folder", "/show-window"} {
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
	if !SignalShow(addr) || r.shown != 1 {
		t.Fatalf("signal: shown = %d", r.shown)
	}

	other := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(other.Close)
	if IsSatellite(strings.TrimPrefix(other.URL, "http://")) {
		t.Fatal("a foreign server is not a satellite")
	}
	other.Close()
	if IsSatellite(strings.TrimPrefix(other.URL, "http://")) || SignalShow(strings.TrimPrefix(other.URL, "http://")) {
		t.Fatal("closed port must not count as instance")
	}
}

func TestFontsAreServedFromTheBinary(t *testing.T) {
	r := newRig(t)
	w := do(t, r.h, "GET", host, "/fonts/ibm-plex-sans-latin-400.woff2", nil, nil)
	if w.Code != 200 || w.Body.Len() < 1000 {
		t.Fatalf("font answer: %d, %d bytes", w.Code, w.Body.Len())
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "font-src 'self'") {
		t.Fatalf("CSP does not allow own fonts: %s", csp)
	}
}
