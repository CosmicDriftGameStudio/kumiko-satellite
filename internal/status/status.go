// Package status serves the local page: setup wizard, status and settings.
package status

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/setup"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

type Source interface {
	Snapshot() syncer.Snapshot
	Retry()
}

// Settings is what the settings page changes; the caller applies it.
type Settings interface {
	AppURL() string
	Folder() string
	SetFolder(folder string) error
	Autostart() bool
	SetAutostart(on bool) error
}

// Options wire the page to the running program. Source returns nil while no
// sync runs (not paired yet). Wizard and Settings may be nil: then the page is
// the plain status view.
type Options struct {
	Version  string
	Source   func() Source
	Wizard   *setup.Wizard
	Settings Settings
	AppURL   func() string
	// ShowWindow reopens the local window; a second start of the program asks for it.
	ShowWindow func()
}

//go:embed layout.html page.html setup.html settings.html
var templateFS embed.FS

//go:embed fonts/*.woff2
var fontFS embed.FS

func parse(name string) *template.Template {
	return template.Must(template.New(name).ParseFS(templateFS, "layout.html", name))
}

var (
	page         = parse("page.html")
	setupPage    = parse("setup.html")
	settingsPage = parse("settings.html")
)

// ValidateAddr only allows loopback listeners: the page offers buttons and shows file names.
func ValidateAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid status address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("status address must be a loopback address such as 127.0.0.1:8765")
	}
	return nil
}

type server struct {
	opt   Options
	token string
}

// Handler builds the plain status handler (used by `run`).
func Handler(src Source, version, appURL string) (http.Handler, error) {
	return NewHandler(Options{Version: version, Source: func() Source { return src }, AppURL: func() string { return appURL }})
}

// NewHandler builds the page handler with a fresh random form token.
func NewHandler(opt Options) (http.Handler, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	s := &server{opt: opt, token: hex.EncodeToString(raw)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.show)
	mux.Handle("GET /fonts/", http.FileServerFS(fontFS))
	mux.HandleFunc("POST /retry", s.post(s.retry))
	if opt.Wizard != nil {
		mux.HandleFunc("POST /setup/pick", s.post(s.setupPick))
		mux.HandleFunc("POST /setup/start", s.post(s.setupStart))
		mux.HandleFunc("POST /setup/open", s.post(s.setupOpen))
		mux.HandleFunc("POST /setup/folder", s.post(s.setupFolder))
		mux.HandleFunc("POST /setup/repair", s.post(s.setupRepair))
	}
	if opt.ShowWindow != nil {
		mux.HandleFunc("GET /instance", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(s.token)) })
		mux.HandleFunc("POST /show-window", s.post(func(w http.ResponseWriter, _ *http.Request) {
			opt.ShowWindow()
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	if opt.Settings != nil {
		mux.HandleFunc("GET /settings", s.settingsShow)
		mux.HandleFunc("POST /settings/pick", s.post(s.settingsPick))
		mux.HandleFunc("POST /settings/folder", s.post(s.settingsFolder))
		mux.HandleFunc("POST /settings/autostart", s.post(s.settingsAutostart))
	}
	return s.guard(mux), nil
}

// guard rejects foreign Host headers: a DNS-rebinding page would send its own
// hostname, while a local browser always sends 127.0.0.1 or localhost.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set(instanceHeader, "1")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func localHost(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return false
	}
	return host == "127.0.0.1" || host == "localhost"
}

// post wraps every state-changing handler: Origin check plus the form token.
func (s *server) post(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil ||
			subtle.ConstantTimeCompare([]byte(r.PostForm.Get("token")), []byte(s.token)) != 1 {
			http.Error(w, "invalid token", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *server) retry(w http.ResponseWriter, r *http.Request) {
	if src := s.source(); src != nil {
		src.Retry()
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) source() Source {
	if s.opt.Source == nil {
		return nil
	}
	return s.opt.Source()
}

func (s *server) appURL() string {
	if s.opt.AppURL == nil {
		return ""
	}
	return s.opt.AppURL()
}

// sameOrigin requires an Origin header: browsers always send it on form POSTs.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && u.Scheme == "http" && strings.EqualFold(u.Host, r.Host)
}

type eventView struct {
	Time    string
	Message string
	Error   bool
}

type view struct {
	Token, Version, StateClass, StateLabel string
	Queued, Failed, Done, Total            int
	Current, LastError, LastSync           string
	Events                                 []eventView
	ShowProgress                           bool
	Checking                               int
	Hint, PairCommand                      string
	ShowRetry                              bool
	ShowCounters                           bool
	ShowRepair, ShowSettings               bool
	Refresh                                int
}

func (s *server) show(w http.ResponseWriter, r *http.Request) {
	src := s.source()
	if s.opt.Wizard != nil {
		st := s.opt.Wizard.State()
		if st.Phase == setup.PhaseIdle && src == nil {
			s.opt.Wizard.Open("")
			st = s.opt.Wizard.State()
		}
		if st.Phase != setup.PhaseIdle {
			s.renderSetup(w, st)
			return
		}
	}
	if src == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	snap := src.Snapshot()
	v := view{
		Token: s.token, Version: s.opt.Version, Queued: snap.Queued, Failed: snap.Failed,
		Done: snap.Done, Total: snap.Total, Current: snap.Current, LastError: snap.LastError,
		ShowProgress: snap.Total > 0, LastSync: "never", Checking: snap.Checking,
		ShowCounters: snap.State != syncer.StateRevoked,
		ShowRetry:    snap.State == syncer.StateQuota || snap.State == syncer.StateError || snap.Failed > 0,
		ShowSettings: s.opt.Settings != nil, Refresh: 5,
	}
	switch snap.State {
	case syncer.StateSyncing:
		v.StateClass, v.StateLabel = "active", "Syncing"
	case syncer.StateChecking:
		v.StateClass, v.StateLabel = "active", "Checking new files"
	case syncer.StateQuota:
		v.StateClass, v.StateLabel = "warn", "Paused: storage in the app is full"
		v.Hint = "An admin has to free up space in the app or raise the limit. Then press “Retry”."
	case syncer.StateRevoked:
		v.StateClass, v.StateLabel = "bad", "Device revoked"
		v.ShowRetry, v.ShowProgress = false, false
		if s.opt.Wizard != nil {
			v.Hint = "This satellite has to be paired with the app again."
			v.ShowRepair = true
		} else {
			v.Hint = "This satellite has to be paired with the app again:"
			v.PairCommand = "kumiko-satellite pair --url " + s.appURL()
		}
	case syncer.StateError:
		v.StateClass, v.StateLabel = "bad", "Error"
	default:
		v.StateClass, v.StateLabel = "ok", "Up to date"
	}
	if !snap.LastSyncAt.IsZero() {
		v.LastSync = snap.LastSyncAt.Local().Format("02.01.2006 15:04:05")
	}
	for _, ev := range snap.Events {
		v.Events = append(v.Events, eventView{ev.Time.Local().Format("02.01. 15:04:05"), ev.Message, ev.Error})
	}
	render(w, page, v)
}

func render(w http.ResponseWriter, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}
