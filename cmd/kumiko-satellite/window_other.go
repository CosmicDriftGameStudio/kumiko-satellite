//go:build !windows && !(darwin && cgo)

package main

// Servers and Linux machines have no window: the caller opens the browser.
func openWindow(string) error { return errNoWindow }
