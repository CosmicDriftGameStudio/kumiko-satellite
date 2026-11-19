package desktop

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// hideWindow stops a console flashing up: the exe is built for the GUI subsystem.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
