package desktop

import (
	"os"
	"path/filepath"
)

// Commands are started by absolute path so a planted binary earlier in PATH
// never runs in their place.

func windowsSystemPath(parts ...string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(append([]string{root, "System32"}, parts...)...)
}

func schtasksPath() string { return windowsSystemPath("schtasks.exe") }

func rundll32Path() string { return windowsSystemPath("rundll32.exe") }

func powershellPath() string {
	return windowsSystemPath("WindowsPowerShell", "v1.0", "powershell.exe")
}

var linuxToolDirs = []string{"/usr/bin", "/usr/local/bin", "/bin", "/run/current-system/sw/bin"} // last: NixOS

// linuxTool returns the first existing executable of name in the fixed system
// folders, or "" when there is none.
func linuxTool(name string) string {
	for _, dir := range linuxToolDirs {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}
