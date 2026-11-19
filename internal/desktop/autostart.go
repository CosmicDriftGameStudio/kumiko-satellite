package desktop

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	taskName     = "Kumiko Satellite"
	launchdLabel = "de.kumiko.satellite"
)

// Plan is what enabling or disabling autostart does: run one command and/or
// write or remove one file. Building it is pure so tests can check it.
type Plan struct {
	Cmd     []string
	File    string
	Content string
	Remove  string
}

func launchAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func desktopEntryPath(home string) string {
	return filepath.Join(home, ".config", "autostart", "kumiko-satellite.desktop")
}

// EnablePlan: Windows uses a per-user logon task (no admin rights), macOS a
// LaunchAgent, Linux an XDG autostart entry.
func EnablePlan(goos, exe, home string) (Plan, error) {
	switch goos {
	case "windows":
		return Plan{Cmd: []string{"schtasks", "/Create", "/SC", "ONLOGON", "/TN", taskName, "/TR", `"` + exe + `"`, "/F"}}, nil
	case "darwin":
		var esc bytes.Buffer
		if err := xml.EscapeText(&esc, []byte(exe)); err != nil {
			return Plan{}, err
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + launchdLabel + `</string>
  <key>ProgramArguments</key><array><string>` + esc.String() + `</string></array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`
		return Plan{File: launchAgentPath(home), Content: plist}, nil
	case "linux":
		if strings.ContainsAny(exe, "\n\r") {
			return Plan{}, errors.New("unsupported program path")
		}
		quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(exe) + `"`
		entry := "[Desktop Entry]\nType=Application\nName=Kumiko Satellite\nExec=" + quoted + "\nTerminal=false\n"
		return Plan{File: desktopEntryPath(home), Content: entry}, nil
	}
	return Plan{}, fmt.Errorf("autostart is not supported on %s", goos)
}

func DisablePlan(goos, home string) (Plan, error) {
	switch goos {
	case "windows":
		return Plan{Cmd: []string{"schtasks", "/Delete", "/TN", taskName, "/F"}}, nil
	case "darwin":
		return Plan{Remove: launchAgentPath(home)}, nil
	case "linux":
		return Plan{Remove: desktopEntryPath(home)}, nil
	}
	return Plan{}, fmt.Errorf("autostart is not supported on %s", goos)
}

func apply(p Plan) error {
	if len(p.Cmd) > 0 {
		cmd := exec.Command(p.Cmd[0], p.Cmd[1:]...)
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", p.Cmd[0], err, strings.TrimSpace(string(out)))
		}
	}
	if p.File != "" {
		if err := os.MkdirAll(filepath.Dir(p.File), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p.File, []byte(p.Content), 0o644); err != nil {
			return err
		}
	}
	if p.Remove != "" {
		if err := os.Remove(p.Remove); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// AutostartEnabled reports whether this machine starts the satellite at logon.
func AutostartEnabled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("schtasks", "/Query", "/TN", taskName)
		hideWindow(cmd)
		return cmd.Run() == nil
	case "darwin":
		_, err = os.Stat(launchAgentPath(home))
	default:
		_, err = os.Stat(desktopEntryPath(home))
	}
	return err == nil
}

// SetAutostart switches autostart for the current user to this executable.
func SetAutostart(on bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var plan Plan
	if on {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		plan, err = EnablePlan(runtime.GOOS, exe, home)
		if err != nil {
			return err
		}
	} else {
		if runtime.GOOS == "windows" && !AutostartEnabled() {
			return nil
		}
		plan, err = DisablePlan(runtime.GOOS, home)
		if err != nil {
			return err
		}
	}
	return apply(plan)
}
