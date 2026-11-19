package main

import (
	"errors"
	"path/filepath"

	"github.com/jchv/go-webview2"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
)

// openWindow blocks until the window is closed. It fails when WebView2 is missing.
func openWindow(url string) error {
	opt := webview2.WebViewOptions{
		AutoFocus:     true,
		WindowOptions: webview2.WindowOptions{Title: windowTitle, Width: windowWidth, Height: windowHeight, Center: true},
	}
	if dir, err := config.Dir(); err == nil {
		opt.DataPath = filepath.Join(dir, "webview2")
	}
	w := webview2.NewWithOptions(opt)
	if w == nil {
		return errors.New("WebView2 is not available")
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
	return nil
}
