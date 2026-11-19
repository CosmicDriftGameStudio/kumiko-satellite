package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/pairing"
)

func fakeApp(t *testing.T) *httptest.Server {
	t.Helper()
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/connectors/v1/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dev","user_code":"AB12-CD34","verification_uri":"https://app/pair","verification_uri_complete":"https://app/pair?code=AB12-CD34","expires_in":600,"interval":5}`))
	})
	mux.HandleFunc("/connectors/v1/token", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"ksat_x","token_type":"Bearer"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type rig struct {
	w      *Wizard
	paired chan config.Config
	folder chan string
}

func newRig(t *testing.T) *rig {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &rig{paired: make(chan config.Config, 1), folder: make(chan string, 1)}
	r.w = New(ctx, func(c config.Config) error { r.paired <- c; return nil }, func(f string) error { r.folder <- f; return nil })
	r.w.NewFlow = func(u string) (*pairing.Flow, error) {
		f, err := pairing.New(u)
		if f != nil {
			f.Sleep = func(context.Context, time.Duration) error { return nil }
		}
		return f, err
	}
	return r
}

func (r *rig) waitPhase(t *testing.T, want Phase) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := r.w.State(); st.Phase == want {
			return st
		}
		runtime.Gosched()
	}
	t.Fatalf("phase never became %s, now %+v", want, r.w.State())
	return State{}
}

func TestBadAddressShowsFieldError(t *testing.T) {
	r := newRig(t)
	r.w.Submit("ftp://nope")
	if st := r.w.State(); st.Phase != PhaseForm || st.URLErr == "" {
		t.Fatalf("%+v", st)
	}
	r.w.Submit("http://example.com")
	if st := r.w.State(); st.URLErr == "" {
		t.Fatal("plain http to a non-loopback host must be refused")
	}
}

func TestPairingThenFolderStep(t *testing.T) {
	r := newRig(t)
	app := fakeApp(t)
	r.w.Submit(app.URL)
	st := r.w.State()
	if st.Phase != PhaseWaiting || st.Code != "AB12-CD34" || st.ConfirmURL != "https://app/pair?code=AB12-CD34" {
		t.Fatalf("waiting state %+v", st)
	}
	cfg := <-r.paired
	if cfg.Token != "ksat_x" || cfg.Folder != "" || cfg.URL != app.URL {
		t.Fatalf("paired config %+v", cfg)
	}
	r.waitPhase(t, PhaseFolder)

	if r.w.SubmitFolder("/", false) {
		t.Fatal("a filesystem root must be refused")
	}
	if st := r.w.State(); st.Phase != PhaseFolder || st.FolderErr == "" {
		t.Fatalf("root folder: %+v", st)
	}
	dir := t.TempDir()
	if !r.w.SubmitFolder(dir, false) || <-r.folder != dir || r.w.State().Phase != PhaseIdle {
		t.Fatalf("valid folder: %+v", r.w.State())
	}
}

func TestRepairKeepsFolderAndSkipsFolderStep(t *testing.T) {
	r := newRig(t)
	app := fakeApp(t)
	r.w.Repair(app.URL, "/data/belege")
	if cfg := <-r.paired; cfg.Folder != "/data/belege" || cfg.Token == "" {
		t.Fatalf("paired config %+v", cfg)
	}
	r.waitPhase(t, PhaseIdle)
}

func TestConfirmLinkFallsBackAndRefusesOddSchemes(t *testing.T) {
	if got := confirmLink(pairing.Authorization{VerificationURI: "https://a/p"}); got != "https://a/p" {
		t.Fatalf("fallback: %q", got)
	}
	if got := confirmLink(pairing.Authorization{VerificationURIComplete: "javascript:alert(1)", VerificationURI: "http://evil.example.com/p"}); got != "" {
		t.Fatalf("unsafe links must not be offered: %q", got)
	}
}

func TestCancelEndsPollLoopAndAllowsNewCode(t *testing.T) {
	var tokenHits, codes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/connectors/v1/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		codes.Add(1)
		_, _ = w.Write([]byte(`{"device_code":"dev","user_code":"AB12-CD34","verification_uri":"https://app/pair","expires_in":600,"interval":5}`))
	})
	mux.HandleFunc("/connectors/v1/token", func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	})
	app := httptest.NewServer(mux)
	t.Cleanup(app.Close)

	r := newRig(t)
	r.w.Submit(app.URL)
	if r.w.State().Phase != PhaseWaiting {
		t.Fatalf("not waiting: %+v", r.w.State())
	}
	r.w.Cancel()
	st := r.w.State()
	if st.Phase != PhaseForm || st.URL != app.URL || st.Code != "" {
		t.Fatalf("after cancel: %+v", st)
	}
	hits := tokenHits.Load()
	time.Sleep(100 * time.Millisecond)
	if tokenHits.Load() != hits {
		t.Fatal("the poll loop kept asking /token after the cancel")
	}
	r.w.Submit(app.URL)
	if codes.Load() != 2 || r.w.State().Phase != PhaseWaiting {
		t.Fatalf("a new pair must fetch a new code: codes %d, %+v", codes.Load(), r.w.State())
	}
	r.w.Cancel()
}
