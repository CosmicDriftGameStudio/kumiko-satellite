package syncer

import (
	"io/fs"
	"path/filepath"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/filter"
)

// Scan walks the folder and queues files whose size and mtime stayed equal over
// two scans, so half-written files are never uploaded.
func (e *Engine) Scan() error {
	present := map[string]observation{}
	err := filepath.WalkDir(e.root, func(p string, d fs.DirEntry, err error) error {
		if p == e.root {
			return err
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// Symlinks and junctions are never followed: they could point out of the folder.
		if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if filter.Skip(d.Name(), info) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(e.root, p)
		if err != nil {
			return nil
		}
		present[filepath.ToSlash(rel)] = observation{info.Size(), info.ModTime().UnixNano()}
		return nil
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scanned = true // also after a failed walk, so an unreadable folder shows its error instead of checking forever
	if err != nil {
		return err
	}
	for rel, obs := range present {
		if st, ok := e.state[rel]; ok && st.Size == obs.size && st.MtimeUnixNano == obs.mtime {
			delete(e.seen, rel)
			continue
		}
		if prev, ok := e.seen[rel]; ok && prev == obs {
			if e.failed[rel] == nil {
				e.enqueueLocked(rel)
			}
			continue
		}
		e.seen[rel] = obs
	}
	for rel := range e.seen {
		if _, ok := present[rel]; !ok {
			delete(e.seen, rel)
		}
	}
	for rel := range e.failed {
		if _, ok := present[rel]; !ok {
			delete(e.failed, rel)
			delete(e.attempts, rel)
		}
	}
	return nil
}
