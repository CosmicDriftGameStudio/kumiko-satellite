package desktop

import (
	"os"
	"testing"
)

func TestAutostartRoundTripsThroughTheRealLogonTask(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("replaces this user's real Kumiko Satellite logon task; runs on the CI Windows runner")
	}
	if err := SetAutostart(true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !AutostartEnabled() {
		t.Fatal("logon task missing after enable")
	}
	if err := SetAutostart(false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if AutostartEnabled() {
		t.Fatal("logon task still present after disable")
	}
	if err := SetAutostart(false); err != nil {
		t.Fatalf("disable without a task: %v", err)
	}
}
