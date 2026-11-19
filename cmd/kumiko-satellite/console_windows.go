package main

import (
	"os"
	"syscall"
)

// attachParentConsole gives the GUI-subsystem exe (-H=windowsgui) its output
// back when it was started from cmd or PowerShell.
func attachParentConsole() {
	const attachParentProcess = ^uintptr(0)
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if ok, _, _ := proc.Call(attachParentProcess); ok == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = f
	}
}
