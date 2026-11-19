package filter

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrForbiddenFolder = errors.New("folder not allowed")

// Env carries the machine-specific protected locations. Empty fields are ignored;
// the built-in rules below still apply, so the check also works in tests on any OS.
type Env struct {
	Home            string
	WinDir          string
	ProgramFiles    string
	ProgramFilesX86 string
	ProgramData     string
}

func EnvFromOS() Env {
	home, _ := os.UserHomeDir()
	if p := os.Getenv("USERPROFILE"); p != "" {
		home = p
	}
	return Env{
		Home:            home,
		WinDir:          os.Getenv("WINDIR"),
		ProgramFiles:    os.Getenv("ProgramFiles"),
		ProgramFilesX86: os.Getenv("ProgramFiles(x86)"),
		ProgramData:     os.Getenv("ProgramData"),
	}
}

// CheckFolder validates a folder on this machine: it must exist, be a directory,
// and neither it nor its symlink-resolved form may be a forbidden location.
func CheckFolder(folder string) error {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return err
	}
	env := EnvFromOS()
	if err := CheckPath(abs, env); err != nil {
		return err
	}
	// A symlink must not smuggle a system folder past the check.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("folder %s: %w", folder, err)
	}
	if err := CheckPath(real, env); err != nil {
		return err
	}
	info, err := os.Stat(real)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a folder", ErrForbiddenFolder, folder)
	}
	return nil
}

func reject(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrForbiddenFolder, fmt.Sprintf(format, args...))
}

// windowsRootSegments are forbidden on every drive, including everything below.
var windowsRootSegments = map[string]bool{
	"windows":                   true,
	"program files":             true,
	"program files (x86)":       true,
	"programdata":               true,
	"system volume information": true,
	"$recycle.bin":              true,
}

var unixRootSegments = map[string]bool{
	"etc": true, "usr": true, "bin": true, "sbin": true, "lib": true, "lib64": true,
	"boot": true, "dev": true, "proc": true, "sys": true, "system": true,
}

// CheckPath is the pure, OS-independent part: it judges the path text only.
func CheckPath(p string, env Env) error {
	n, ok := normalize(p)
	if !ok {
		return reject("%q is not an absolute path", p)
	}
	if n.root {
		return reject("%q is a drive, share or filesystem root; pick a folder inside it", p)
	}
	if n.windows {
		if err := checkWindows(n, p); err != nil {
			return err
		}
	} else if unixRootSegments[n.segs[0]] {
		return reject("%q is a system folder", p)
	}
	return checkEnv(n, p, env)
}

type normPath struct {
	windows bool
	root    bool
	prefix  string   // "c:" or "//server/share" or ""
	segs    []string // lower-case segments below the prefix
}

func (n normPath) String() string { return n.prefix + "/" + strings.Join(n.segs, "/") }

func (n normPath) under(root normPath) bool {
	if n.windows != root.windows || n.prefix != root.prefix || len(n.segs) < len(root.segs) {
		return false
	}
	for i, s := range root.segs {
		if n.segs[i] != s {
			return false
		}
	}
	return true
}

func normalize(p string) (normPath, bool) {
	var n normPath
	s := strings.ToLower(p)
	switch {
	case len(s) >= 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] == ':':
		rest := strings.ReplaceAll(s[2:], `\`, "/")
		if !strings.HasPrefix(rest, "/") {
			return n, false // "c:foo" is relative to the drive's current folder
		}
		n.windows, n.prefix = true, s[:2]
		return n.withSegs(rest), true
	case strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, "//"):
		parts := splitClean(strings.ReplaceAll(s, `\`, "/"))
		if len(parts) > 0 && (parts[0] == "?" || parts[0] == ".") {
			return n, false // device paths bypass every check below
		}
		n.windows = true
		if len(parts) < 2 {
			n.prefix, n.root = "//"+strings.Join(parts, "/"), true
			return n, true
		}
		n.prefix = "//" + parts[0] + "/" + parts[1]
		return n.withSegs("/" + strings.Join(parts[2:], "/")), true
	case strings.HasPrefix(s, "/"):
		return n.withSegs(p), true // unix paths are case-sensitive; compare lower-case anyway
	}
	return n, false
}

func (n normPath) withSegs(rest string) normPath {
	n.segs = splitClean(strings.ToLower(rest))
	n.root = len(n.segs) == 0
	return n
}

func splitClean(s string) []string {
	c := strings.Trim(path.Clean("/"+s), "/")
	if c == "" {
		return nil
	}
	return strings.Split(c, "/")
}

func checkWindows(n normPath, orig string) error {
	if strings.HasPrefix(n.prefix, "//") {
		return nil // network shares: only the share root is refused above
	}
	if windowsRootSegments[n.segs[0]] {
		return reject("%q is a Windows system folder", orig)
	}
	if n.segs[0] == "users" && len(n.segs) <= 2 {
		return reject("%q is the user profile or the folder of all profiles", orig)
	}
	for _, s := range n.segs {
		if s == "appdata" {
			return reject("%q is inside AppData", orig)
		}
	}
	return nil
}

func checkEnv(n normPath, orig string, env Env) error {
	for _, sys := range []string{env.WinDir, env.ProgramFiles, env.ProgramFilesX86, env.ProgramData} {
		if r, ok := normalize(sys); sys != "" && ok && n.under(r) {
			return reject("%q is a system folder", orig)
		}
	}
	if env.Home != "" {
		// The profile itself and every folder above it contain the whole profile.
		if h, ok := normalize(env.Home); ok && h.under(n) {
			return reject("%q is or contains your user profile; pick a subfolder", orig)
		}
	}
	return nil
}
