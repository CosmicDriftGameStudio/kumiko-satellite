package status

import (
	"errors"
	"net/http"
	"strings"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/filter"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/setup"
)

type settingsView struct {
	Token, Version, Folder, FolderErr, Notice string
	Autostart, CanPick                        bool
	Refresh                                   int
}

func (s *server) renderSettings(w http.ResponseWriter, folder, folderErr, notice string) {
	render(w, settingsPage, settingsView{
		Token: s.token, Version: s.opt.Version, Folder: folder, FolderErr: folderErr, Notice: notice,
		Autostart: s.opt.Settings.Autostart(), CanPick: desktop.DialogAvailable(),
	})
}

func (s *server) settingsShow(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, s.opt.Settings.Folder(), "", "")
}

func (s *server) settingsPick(w http.ResponseWriter, r *http.Request) {
	folder := r.PostForm.Get("folder")
	picked, err := desktop.PickFolder(r.Context())
	switch {
	case err == nil:
		folder = picked
		s.renderSettings(w, folder, setup.FolderMessage(filter.CheckFolder(folder)), "")
	case errors.Is(err, desktop.ErrCancelled):
		s.renderSettings(w, folder, "", "")
	default:
		s.renderSettings(w, folder, setup.MsgNoDialog, "")
	}
}

func (s *server) settingsFolder(w http.ResponseWriter, r *http.Request) {
	folder := strings.TrimSpace(r.PostForm.Get("folder"))
	if folder == "" {
		s.renderSettings(w, folder, "Please choose a folder.", "")
		return
	}
	if msg := setup.FolderMessage(filter.CheckFolder(folder)); msg != "" {
		s.renderSettings(w, folder, msg, "")
		return
	}
	if err := s.opt.Settings.SetFolder(folder); err != nil {
		s.renderSettings(w, folder, "The folder could not be saved.", "")
		return
	}
	s.renderSettings(w, folder, "", "Folder saved, syncing continues with the new folder.")
}

func (s *server) settingsAutostart(w http.ResponseWriter, r *http.Request) {
	on := r.PostForm.Get("autostart") == "on"
	if err := s.opt.Settings.SetAutostart(on); err != nil {
		s.renderSettings(w, s.opt.Settings.Folder(), "", "Autostart could not be changed.")
		return
	}
	s.renderSettings(w, s.opt.Settings.Folder(), "", "Saved.")
}
