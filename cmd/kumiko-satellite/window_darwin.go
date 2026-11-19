//go:build darwin && cgo

package main

import webview "github.com/webview/webview_go"

// openWindow blocks until the window is closed; it must run on the main thread.
func openWindow(url string) error {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(windowTitle)
	w.SetSize(windowWidth, windowHeight, webview.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}
