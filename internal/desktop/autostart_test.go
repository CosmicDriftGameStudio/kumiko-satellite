package desktop

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnablePlanWindowsUsesFixedSchtasksArguments(t *testing.T) {
	p, err := EnablePlan("windows", `C:\Tools\kumiko satellite.exe`, `C:\Users\a`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"schtasks", "/Create", "/SC", "ONLOGON", "/TN", "Kumiko Satellite", "/TR", `"C:\Tools\kumiko satellite.exe"`, "/F"}
	if !reflect.DeepEqual(p.Cmd, want) {
		t.Fatalf("cmd = %q, want %q", p.Cmd, want)
	}
	d, _ := DisablePlan("windows", `C:\Users\a`)
	if !reflect.DeepEqual(d.Cmd, []string{"schtasks", "/Delete", "/TN", "Kumiko Satellite", "/F"}) {
		t.Fatalf("disable cmd = %q", d.Cmd)
	}
}

func TestEnablePlanWritesLaunchAgentAndDesktopEntry(t *testing.T) {
	mac, err := EnablePlan("darwin", "/Applications/A&B/kumiko-satellite", "/Users/a")
	if err != nil {
		t.Fatal(err)
	}
	if mac.File != filepath.Join("/Users/a", "Library", "LaunchAgents", "de.kumiko.satellite.plist") ||
		!strings.Contains(mac.Content, "<string>/Applications/A&amp;B/kumiko-satellite</string>") ||
		!strings.Contains(mac.Content, "<key>RunAtLoad</key><true/>") || mac.Cmd != nil {
		t.Fatalf("plan %+v", mac)
	}
	lin, err := EnablePlan("linux", "/opt/my sat/kumiko-satellite", "/home/a")
	if err != nil {
		t.Fatal(err)
	}
	if lin.File != filepath.Join("/home/a", ".config", "autostart", "kumiko-satellite.desktop") ||
		!strings.Contains(lin.Content, `Exec="/opt/my sat/kumiko-satellite"`) {
		t.Fatalf("plan %+v", lin)
	}
	if d, _ := DisablePlan("linux", "/home/a"); d.Remove != lin.File {
		t.Fatalf("disable removes %q, want %q", d.Remove, lin.File)
	}
	if _, err := EnablePlan("plan9", "x", "y"); err == nil {
		t.Fatal("unsupported OS must fail")
	}
}
