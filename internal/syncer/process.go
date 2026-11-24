package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/api"
)

type Uploader interface {
	Upload(ctx context.Context, u api.Upload) error
}

var (
	errGone    = errors.New("file vanished")
	errChanged = errors.New("file changed while reading")
)

// Process uploads the queue one file at a time until it is empty or blocked.
func (e *Engine) Process(ctx context.Context) {
	e.mu.Lock()
	e.promoteDueLocked()
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.processing, e.current = false, ""
		e.mu.Unlock()
	}()
	for ctx.Err() == nil {
		rel, ok := e.next()
		if !ok {
			return
		}
		e.finish(rel, e.uploadOne(ctx, rel))
	}
}

func (e *Engine) promoteDueLocked() {
	for rel, f := range e.failed {
		if !f.permanent && !e.now().Before(f.next) {
			delete(e.failed, rel)
			e.enqueueLocked(rel)
		}
	}
}

func (e *Engine) next() (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.blocked != "" || len(e.queue) == 0 {
		return "", false
	}
	rel := e.queue[0]
	e.queue = e.queue[1:]
	delete(e.queued, rel)
	e.processing, e.current = true, rel
	return rel, true
}

func (e *Engine) uploadOne(ctx context.Context, rel string) error {
	// The path may have been swapped for a link since the scan; os.Root refuses
	// to resolve outside the folder and Lstat refuses links at the leaf.
	root, err := os.OpenRoot(e.root)
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.FromSlash(rel)
	linkInfo, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !linkInfo.Mode().IsRegular()) {
		return errGone
	}
	if err != nil {
		return err
	}
	f, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return errGone
	}
	if err != nil {
		return err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(linkInfo, before) {
		return errChanged
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	after, err := root.Stat(name)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return errChanged
	}
	sum := hex.EncodeToString(h.Sum(nil))
	obs := entry{Size: before.Size(), MtimeUnixNano: before.ModTime().UnixNano(), SHA256: sum}

	e.mu.Lock()
	prev, had := e.state[rel]
	e.mu.Unlock()
	if had && prev.SHA256 == sum {
		e.record(rel, obs, false) // only the mtime moved; the server has these bytes
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	err = e.up.Upload(ctx, api.Upload{
		Path: rel, SHA256: sum, Mtime: before.ModTime(), Size: before.Size(), Body: f,
		ContentType: mime.TypeByExtension(filepath.Ext(rel)),
	})
	if err == nil {
		e.record(rel, obs, true)
	}
	return err
}

func (e *Engine) record(rel string, obs entry, uploaded bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state[rel] = obs
	delete(e.seen, rel)
	delete(e.attempts, rel)
	if uploaded {
		e.logLocked("Uploaded: "+rel, false)
	}
	e.done++
	e.lastSync = e.now()
	if len(e.failed) == 0 {
		e.lastErr = ""
	}
	if err := e.saveStateLocked(); err != nil {
		e.logLocked("State file not writable: "+err.Error(), true)
	}
}

func (e *Engine) saveStateLocked() error {
	raw, err := json.Marshal(e.state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.statePath), 0o700); err != nil {
		return err
	}
	tmp := e.statePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.statePath)
}

func (e *Engine) finish(rel string, err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if errors.Is(err, errGone) {
		e.total--
		return
	}
	if errors.Is(err, errChanged) {
		// Still being written: let the next two scans decide again.
		e.total--
		delete(e.seen, rel)
		return
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Revoked():
			e.blocked, e.blockedAs = messageRevoked, StateRevoked
			e.queueBackLocked(rel)
			e.logLocked(messageRevoked, true)
			return
		case apiErr.Quota():
			e.blocked, e.blockedAs = messageQuota, StateQuota
			e.queueBackLocked(rel)
			e.logLocked(messageQuota+", uploads paused until “Retry”", true)
			return
		case apiErr.Rejected():
			e.failLocked(rel, &failure{message: err.Error(), permanent: true})
			return
		}
	}
	e.attempts[rel]++
	wait := backoffs[min(e.attempts[rel], len(backoffs))-1]
	if apiErr != nil && apiErr.RetryAfter > 0 {
		wait = apiErr.RetryAfter
	}
	e.failLocked(rel, &failure{message: err.Error(), next: e.now().Add(wait)})
}

// queueBackLocked keeps a file that was not judged yet at the head of the queue.
func (e *Engine) queueBackLocked(rel string) {
	e.queued[rel] = true
	e.queue = append([]string{rel}, e.queue...)
}

func (e *Engine) failLocked(rel string, f *failure) {
	e.failed[rel] = f
	e.total--
	e.lastErr = rel + ": " + f.message
	e.logLocked("Error at "+rel+": "+f.message, true)
}

// Heartbeater is the part of the API client the heartbeat loop needs.
type Heartbeater interface {
	Heartbeat(ctx context.Context, version string, sync *api.SyncInfo) (api.HeartbeatReply, error)
}

// wireState maps to the states the server accepts (idle, syncing, error);
// an unknown state is reported as error rather than passed through.
func wireState(s State) string {
	switch s {
	case StateIdle:
		return "idle"
	case StateSyncing, StateChecking:
		return "syncing"
	case StateError, StateRejected, StateQuota, StateRevoked:
		return "error"
	default:
		return "error"
	}
}

// SendHeartbeat reports the state once; retry:true requeues the failed files.
func (e *Engine) SendHeartbeat(ctx context.Context, hb Heartbeater, version string) {
	s := e.Snapshot()
	info := &api.SyncInfo{
		State: wireState(s.State), Queued: s.Queued, Failed: s.Failed, LastError: truncateRunes(s.LastError, 200),
	}
	if !s.LastSyncAt.IsZero() {
		info.LastSyncAt = s.LastSyncAt.UTC().Format(time.RFC3339)
	}
	reply, err := hb.Heartbeat(ctx, version, info)
	var apiErr *api.Error
	switch {
	case errors.As(err, &apiErr) && apiErr.Revoked():
		e.NoteRevoked()
	case err != nil:
		e.mu.Lock()
		e.logLocked("Heartbeat failed: "+err.Error(), true)
		e.mu.Unlock()
	default:
		e.mu.Lock()
		e.deviceName = reply.Name
		e.mu.Unlock()
		if reply.Retry {
			e.Retry()
		}
	}
}

// DeviceName is the name the last heartbeat reported; empty until then or
// when the server does not send one.
func (e *Engine) DeviceName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deviceName
}

// RunHeartbeat sends a heartbeat immediately and then every interval.
func (e *Engine) RunHeartbeat(ctx context.Context, hb Heartbeater, version string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		e.SendHeartbeat(ctx, hb, version)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
