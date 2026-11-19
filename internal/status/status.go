// Package status serves the small local status page with the retry button.
package status

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

type Source interface {
	Snapshot() syncer.Snapshot
	Retry()
}

//go:embed page.html
var pageHTML string

var page = template.Must(template.New("page").Parse(pageHTML))

// ValidateAddr only allows loopback listeners: the page offers a button and shows file names.
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
	src     Source
	version string
	token   string
}

// Handler builds the page handler with a fresh random form token.
func Handler(src Source, version string) (http.Handler, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	s := &server{src: src, version: version, token: hex.EncodeToString(raw)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.show)
	mux.HandleFunc("POST /retry", s.retry)
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
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
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

func (s *server) retry(w http.ResponseWriter, r *http.Request) {
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
	s.src.Retry()
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
}

func (s *server) show(w http.ResponseWriter, r *http.Request) {
	snap := s.src.Snapshot()
	v := view{
		Token: s.token, Version: s.version, Queued: snap.Queued, Failed: snap.Failed,
		Done: snap.Done, Total: snap.Total, Current: snap.Current, LastError: snap.LastError,
		ShowProgress: snap.Total > 0, LastSync: "noch nie",
	}
	switch snap.State {
	case syncer.StateSyncing:
		v.StateClass, v.StateLabel = "busy", "Synchronisiert gerade"
	case syncer.StateError:
		v.StateClass, v.StateLabel = "err", "Fehler"
	default:
		v.StateClass, v.StateLabel = "ok", "Alles aktuell"
	}
	if !snap.LastSyncAt.IsZero() {
		v.LastSync = snap.LastSyncAt.Local().Format("02.01.2006 15:04:05")
	}
	for _, ev := range snap.Events {
		v.Events = append(v.Events, eventView{ev.Time.Local().Format("02.01. 15:04:05"), ev.Message, ev.Error})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.Execute(w, v); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}
