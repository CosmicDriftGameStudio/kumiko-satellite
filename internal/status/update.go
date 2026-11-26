package status

import "net/http"

// UpdateOptions wire the self-update. Available reports a newer version. Start
// downloads and installs it and arranges the restart; it blocks until done and
// returns the error the page shows. Without Start (macOS) the page links to PageURL.
type UpdateOptions struct {
	Available func() (version string, ok bool)
	Start     func() error
	PageURL   string
}

func (s *server) updateView() (version string, canStart bool, link string) {
	u := s.opt.Update
	if u == nil || u.Available == nil {
		return "", false, ""
	}
	v, ok := u.Available()
	if !ok {
		return "", false, ""
	}
	return v, u.Start != nil, u.PageURL
}

func (s *server) updateError() string {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	return s.updateErr
}

func (s *server) setUpdateError(msg string) {
	s.updateMu.Lock()
	s.updateErr = msg
	s.updateMu.Unlock()
}

// update answers first, then installs; a failure is kept for the status page
// and the satellite keeps running.
func (s *server) update(w http.ResponseWriter, r *http.Request) {
	version, _, _ := s.updateView()
	render(w, updatingPage, struct {
		Version string
		Refresh int
	}{version, 0})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	s.setUpdateError("")
	if err := s.opt.Update.Start(); err != nil {
		s.setUpdateError("Update failed: " + err.Error())
	}
}
