package status

import (
	"errors"
	"net/http"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/setup"
)

type setupView struct {
	Token, Version string
	State          setup.State
	Waiting        bool
	Failed         bool
	FolderStep     bool
	CanPick        bool
	Refresh        int
	MinutesLeft    int
}

func (s *server) renderSetup(w http.ResponseWriter, st setup.State) {
	v := setupView{
		Token: s.token, Version: s.opt.Version, State: st,
		Waiting: st.Phase == setup.PhaseWaiting, Failed: st.Phase == setup.PhaseFailed,
		FolderStep: st.Phase == setup.PhaseFolder, CanPick: desktop.DialogAvailable(),
	}
	if v.Waiting {
		v.Refresh = 3
		if left := time.Until(st.ExpiresAt); !st.ExpiresAt.IsZero() && left > 0 {
			v.MinutesLeft = int((left + time.Minute - 1) / time.Minute)
		}
	}
	render(w, setupPage, v)
}

func (s *server) setupStart(w http.ResponseWriter, r *http.Request) {
	s.opt.Wizard.Submit(r.PostForm.Get("url"))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setupOpen opens the confirm link in the default browser: the link comes from
// the wizard state, never from the request.
func (s *server) setupOpen(w http.ResponseWriter, r *http.Request) {
	if u := s.opt.Wizard.State().ConfirmURL; u != "" {
		_ = desktop.OpenBrowser(u)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) setupPick(w http.ResponseWriter, r *http.Request) {
	folder, noAutostart := r.PostForm.Get("folder"), r.PostForm.Get("autostart") != "on"
	picked, err := desktop.PickFolder(r.Context())
	if err == nil {
		folder = picked
	}
	s.opt.Wizard.DraftFolder(folder, noAutostart)
	if err != nil && !errors.Is(err, desktop.ErrCancelled) {
		s.opt.Wizard.FolderError(setup.MsgNoDialog)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) setupFolder(w http.ResponseWriter, r *http.Request) {
	autostart := r.PostForm.Get("autostart") == "on"
	if s.opt.Wizard.SubmitFolder(r.PostForm.Get("folder"), !autostart) && autostart && s.opt.Settings != nil {
		_ = s.opt.Settings.SetAutostart(true) // the settings page shows the real state if this fails
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setupRepair re-pairs with the saved address; the folder stays.
func (s *server) setupRepair(w http.ResponseWriter, r *http.Request) {
	if s.opt.Settings != nil {
		s.opt.Wizard.Repair(s.opt.Settings.AppURL(), s.opt.Settings.Folder())
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
