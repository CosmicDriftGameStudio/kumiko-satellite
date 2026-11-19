// Package syncer scans a folder and delivers new or changed files one by one.
package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"
)

type State string

const (
	StateIdle    State = "idle"
	StateSyncing State = "syncing"
	StateError   State = "error"
	// StateChecking: files were seen but have not stayed unchanged over two scans yet.
	StateChecking State = "checking"
	StateQuota    State = "quota"
	StateRevoked  State = "revoked"
)

const (
	maxEvents      = 200
	messageRevoked = "Gerät gesperrt, neu koppeln"
	messageQuota   = "Speicherplatz in der App erschöpft"
)

var backoffs = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}

type Event struct {
	Time    time.Time
	Message string
	Error   bool
}

type Snapshot struct {
	State      State
	Queued     int
	Checking   int // seen, waiting for the second matching scan
	Failed     int
	Done       int
	Total      int
	Current    string
	LastError  string
	LastSyncAt time.Time
	Events     []Event // newest first
}

// entry is what the last successful upload of a path looked like.
type entry struct {
	Size          int64  `json:"size"`
	MtimeUnixNano int64  `json:"mtimeUnixNano"`
	SHA256        string `json:"sha256"`
}

type observation struct{ size, mtime int64 }

type failure struct {
	message   string
	next      time.Time
	permanent bool // the server refused this file; only a manual retry helps
}

type Engine struct {
	root      string
	statePath string
	up        Uploader
	now       func() time.Time

	mu         sync.Mutex
	state      map[string]entry
	seen       map[string]observation
	queue      []string
	queued     map[string]bool
	failed     map[string]*failure
	attempts   map[string]int
	blocked    string
	blockedAs  State // StateQuota or StateRevoked while blocked != ""
	current    string
	processing bool
	done       int
	total      int
	lastErr    string
	lastSync   time.Time
	events     []Event // oldest first, capped
	wake       chan struct{}
}

func New(root, statePath string, up Uploader) *Engine {
	e := &Engine{
		root: root, statePath: statePath, up: up, now: time.Now,
		state: map[string]entry{}, seen: map[string]observation{},
		queued: map[string]bool{}, failed: map[string]*failure{}, attempts: map[string]int{},
		wake: make(chan struct{}, 1),
	}
	if raw, err := os.ReadFile(statePath); err == nil {
		if json.Unmarshal(raw, &e.state) != nil {
			e.state = map[string]entry{}
			e.logLocked("Statusdatei unlesbar, alle Dateien werden neu geprüft", true)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		e.logLocked("Statusdatei nicht lesbar: "+err.Error(), true)
	}
	return e
}

func (e *Engine) logLocked(msg string, isErr bool) {
	e.events = append(e.events, Event{Time: e.now(), Message: msg, Error: isErr})
	if len(e.events) > maxEvents {
		e.events = append(e.events[:0], e.events[len(e.events)-maxEvents:]...)
	}
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Snapshot{
		State: StateIdle, Queued: len(e.queue), Checking: e.checkingLocked(), Failed: len(e.failed), Done: e.done, Total: e.total,
		Current: e.current, LastError: e.lastErr, LastSyncAt: e.lastSync,
	}
	switch {
	case e.blocked != "":
		s.State, s.LastError = e.blockedAs, e.blocked
	case len(e.failed) > 0:
		s.State = StateError
	case e.processing:
		s.State = StateSyncing
	case len(e.queue) > 0 || s.Checking > 0:
		s.State = StateChecking
	}
	s.Events = make([]Event, len(e.events))
	for i, ev := range e.events {
		s.Events[len(e.events)-1-i] = ev
	}
	return s
}

// checkingLocked counts changed files that wait for their second matching scan.
func (e *Engine) checkingLocked() int {
	n := 0
	for rel, obs := range e.seen {
		if st, ok := e.state[rel]; ok && st.Size == obs.size && st.MtimeUnixNano == obs.mtime {
			continue
		}
		if !e.queued[rel] && e.failed[rel] == nil && rel != e.current {
			n++
		}
	}
	return n
}

// Retry clears a block and puts every failed file back into the queue.
func (e *Engine) Retry() {
	e.mu.Lock()
	e.blocked, e.blockedAs, e.lastErr = "", "", ""
	n := 0
	for rel := range e.failed {
		delete(e.failed, rel)
		delete(e.attempts, rel)
		e.enqueueLocked(rel)
		n++
	}
	e.logLocked("Erneut versuchen: "+strconv.Itoa(n)+" Datei(en) zurück in der Warteschlange", false)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// NoteRevoked blocks uploads after the server rejected the token.
func (e *Engine) NoteRevoked() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.blocked != messageRevoked {
		e.blocked, e.blockedAs = messageRevoked, StateRevoked
		e.logLocked(messageRevoked, true)
	}
}

func (e *Engine) enqueueLocked(rel string) {
	if e.queued[rel] {
		return
	}
	if len(e.queue) == 0 && !e.processing {
		e.done, e.total = 0, 0
	}
	e.queued[rel] = true
	e.queue = append(e.queue, rel)
	e.total++
}

// Run scans every interval and uploads in between, until ctx ends.
func (e *Engine) Run(ctx context.Context, scanEvery time.Duration) {
	// ponytail: fsnotify later, if the polling latency starts to bother users.
	t := time.NewTicker(scanEvery)
	defer t.Stop()
	for {
		if err := e.Scan(); err != nil {
			e.mu.Lock()
			e.logLocked("Ordner konnte nicht gelesen werden: "+err.Error(), true)
			e.mu.Unlock()
		}
		e.Process(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.wake:
		}
	}
}
