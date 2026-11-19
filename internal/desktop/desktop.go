// Package desktop wraps the few OS integrations of the satellite: native folder
// dialog, opening the default browser and autostart. Every command runs with
// fixed arguments, no user input ever reaches a command line.
package desktop

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	// ErrCancelled means the user closed the folder dialog without choosing.
	ErrCancelled = errors.New("no folder chosen")
	// ErrNoDialog means this machine has no native folder dialog.
	ErrNoDialog = errors.New("no folder dialog available")
)

// Replaceable so tests never open real dialogs or browsers.
var (
	PickFolder      = pickFolderNative
	OpenBrowser     = openBrowserNative
	DialogAvailable = dialogAvailableNative
)

const windowsPickScript = `Add-Type -AssemblyName System.Windows.Forms; ` +
	`$d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
	`if ($d.ShowDialog() -eq 'OK') { [Console]::Out.Write($d.SelectedPath) }`

func dialogAvailableNative() bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return true
	}
	_, err := exec.LookPath("zenity")
	return err == nil
}

func pickFolderNative(ctx context.Context) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-Command", windowsPickScript)
	case "darwin":
		cmd = exec.CommandContext(ctx, "osascript", "-e", "POSIX path of (choose folder)")
	default:
		cmd = exec.CommandContext(ctx, "zenity", "--file-selection", "--directory")
	}
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return "", ErrNoDialog
		case errors.As(err, &exit):
			return "", ErrCancelled
		}
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", ErrCancelled
	}
	return filepath.Clean(p), nil
}

func openBrowserNative(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
