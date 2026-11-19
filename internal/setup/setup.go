// Package setup holds the pairing wizard behind the local setup page: it
// validates address and folder, runs the device flow and reports its state.
package setup

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/api"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/filter"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/pairing"
)

type Phase string

const (
	PhaseIdle    Phase = "idle" // paired, folder set, sync runs: the wizard is not shown
	PhaseForm    Phase = "form" // step 1: address
	PhaseWaiting Phase = "waiting"
	PhaseFailed  Phase = "failed"
	PhaseFolder  Phase = "folder" // step 2: folder, after the device is paired
)

const (
	msgURL       = "Please enter the address of your app, e.g. https://app.company.com (http is only allowed for localhost)."
	msgUnreached = "No Kumiko app answered at this address, or it does not support pairing."
	msgNoFolder  = "Please choose a folder."
	msgFolder    = "Please choose a folder of its own, not a whole drive or a system folder."
	msgMissing   = "This folder does not exist or is not a folder."
	msgSaveFail  = "The folder could not be saved."
	// MsgNoDialog is shown at the folder field when the native dialog fails.
	MsgNoDialog = "The folder dialog is not available, please type the path."
)

// State is everything the setup page renders.
type State struct {
	Phase             Phase
	URL, URLErr       string
	Code, ConfirmURL  string
	Failure           string
	Folder, FolderErr string
	NoAutostart       bool
	ExpiresAt         time.Time
}

type Wizard struct {
	// NewFlow, OnPaired and OnFolder are set by the caller; tests replace NewFlow.
	NewFlow  func(appURL string) (*pairing.Flow, error)
	OnPaired func(config.Config) error // token arrives; Folder is set only when re-pairing
	OnFolder func(folder string) error

	ctx    context.Context
	mu     sync.Mutex
	st     State
	keep   string // folder to keep when re-pairing
	gen    int
	cancel context.CancelFunc
	done   chan struct{} // closed when the poll loop of the current pairing has ended
}

func New(ctx context.Context, onPaired func(config.Config) error, onFolder func(string) error) *Wizard {
	return &Wizard{ctx: ctx, NewFlow: pairing.New, OnPaired: onPaired, OnFolder: onFolder, st: State{Phase: PhaseIdle}}
}

func (w *Wizard) State() State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.st
}

// Open shows step 1 with a prefilled address.
func (w *Wizard) Open(appURL string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	w.keep = ""
	w.st = State{Phase: PhaseForm, URL: appURL}
}

// OpenFolder shows step 2 for a device that is paired but has no folder yet.
func (w *Wizard) OpenFolder(appURL, folder string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	w.st = State{Phase: PhaseFolder, URL: appURL, Folder: folder}
}

// Repair re-pairs only: the folder stays what it was.
func (w *Wizard) Repair(appURL, folder string) {
	w.mu.Lock()
	w.keep = folder
	w.mu.Unlock()
	w.Submit(appURL)
}

// DraftFolder keeps what the user typed in step 2 while a dialog ran.
func (w *Wizard) DraftFolder(folder string, noAutostart bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.st.Phase, w.st.Folder, w.st.NoAutostart, w.st.FolderErr = PhaseFolder, folder, noAutostart, ""
	if folder != "" {
		w.st.FolderErr = FolderMessage(filter.CheckFolder(folder))
	}
}

// FolderError puts a message at the folder field.
func (w *Wizard) FolderError(msg string) {
	w.mu.Lock()
	w.st.FolderErr = msg
	w.mu.Unlock()
}

// SubmitFolder checks the folder, starts the sync and leaves the wizard.
func (w *Wizard) SubmitFolder(folder string, noAutostart bool) bool {
	folder = strings.TrimSpace(folder)
	w.DraftFolder(folder, noAutostart)
	if folder == "" {
		w.FolderError(msgNoFolder)
	}
	if w.State().FolderErr != "" {
		return false
	}
	if err := w.OnFolder(folder); err != nil {
		w.FolderError(msgSaveFail)
		return false
	}
	w.mu.Lock()
	w.st = State{Phase: PhaseIdle}
	w.mu.Unlock()
	return true
}

func (w *Wizard) stopLocked() {
	w.gen++
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
}

// Submit validates the address and starts a pairing; the code shows up in the same view.
func (w *Wizard) Submit(appURL string) {
	appURL = strings.TrimSpace(appURL)
	st := State{Phase: PhaseForm, URL: appURL}
	var flow *pairing.Flow
	var auth pairing.Authorization
	if _, err := api.Endpoint(appURL); err != nil {
		st.URLErr = msgURL
	} else {
		var err error
		if flow, err = w.NewFlow(appURL); err == nil {
			ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
			auth, err = flow.Start(ctx)
			cancel()
		}
		if err != nil {
			st.URLErr = msgUnreached
		}
	}

	w.mu.Lock()
	w.stopLocked()
	if st.URLErr != "" {
		w.st = st
		w.mu.Unlock()
		return
	}
	pollCtx, cancel := context.WithCancel(w.ctx)
	w.cancel = cancel
	gen := w.gen
	st.Phase, st.Code, st.ConfirmURL = PhaseWaiting, auth.UserCode, confirmLink(auth)
	if auth.ExpiresIn > 0 {
		st.ExpiresAt = time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	}
	w.st = st
	keep := w.keep
	w.mu.Unlock()
	done := make(chan struct{})
	w.done = done
	go func() {
		defer close(done)
		w.poll(pollCtx, gen, flow, auth, config.Config{URL: appURL, Folder: keep})
	}()
}

// Cancel drops a running pairing: the poll loop ends, the code is discarded
// locally and the address field is free again.
func (w *Wizard) Cancel() {
	w.mu.Lock()
	if w.st.Phase != PhaseWaiting {
		w.mu.Unlock()
		return
	}
	w.stopLocked()
	done := w.done
	w.st = State{Phase: PhaseForm, URL: w.st.URL}
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (w *Wizard) poll(ctx context.Context, gen int, flow *pairing.Flow, auth pairing.Authorization, cfg config.Config) {
	token, err := flow.Poll(ctx, auth)
	if ctx.Err() != nil && !errors.Is(err, pairing.ErrExpired) {
		return
	}
	failure := ""
	if err != nil {
		failure = pollMessage(err)
	} else {
		cfg.Token = token
		if err := w.OnPaired(cfg); err != nil {
			failure = "The access could not be saved: " + err.Error()
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if gen != w.gen {
		return
	}
	switch {
	case failure != "":
		w.st.Phase, w.st.Failure = PhaseFailed, failure
	case cfg.Folder != "":
		w.st = State{Phase: PhaseIdle}
	default:
		w.st = State{Phase: PhaseFolder, URL: cfg.URL}
	}
}

func pollMessage(err error) string {
	if errors.Is(err, pairing.ErrExpired) {
		return "The code expired. Please get a new code."
	}
	if strings.Contains(err.Error(), "declined") {
		return "Pairing was declined in the app. Get a new code if needed."
	}
	return "Pairing failed. Please get a new code."
}

// confirmLink prefers the link that already carries the code. The server sends
// it, so only https (or http to loopback) is ever offered as a link.
func confirmLink(a pairing.Authorization) string {
	for _, raw := range []string{a.VerificationURIComplete, a.VerificationURI} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		if u.Scheme == "https" || (u.Scheme == "http" && api.IsLoopbackHost(u.Hostname())) {
			return u.String()
		}
	}
	return ""
}

// FolderMessage turns a folder check error into text for the field; "" means fine.
func FolderMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, filter.ErrForbiddenFolder) && !strings.Contains(err.Error(), "is not a folder"):
		return msgFolder
	default:
		return msgMissing
	}
}
