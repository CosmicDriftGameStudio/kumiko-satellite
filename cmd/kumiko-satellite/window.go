package main

import (
	"errors"
	"log"
	"runtime"
	"sync/atomic"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
)

const (
	windowTitle  = "Kumiko Satellite"
	windowWidth  = 640
	windowHeight = 720
)

var errNoWindow = errors.New("no window on this platform")

// GUI toolkits want one fixed thread, so windows are opened by the main goroutine.
func init() { runtime.LockOSThread() }

var (
	windowRequests = make(chan func() string, 1)
	windowOpen     atomic.Bool
)

// requestWindow asks the main goroutine to open the window unless one is open.
// The URL is built when the window opens, so its login nonce is fresh.
func requestWindow(url func() string) {
	if windowOpen.Load() {
		return
	}
	select {
	case windowRequests <- url:
	default:
	}
}

// serveWindows runs on the main goroutine until the program is done. Closing a
// window does not stop the sync; a later start asks for a new window.
func serveWindows(done <-chan int) int {
	for {
		select {
		case code := <-done:
			return code
		case makeURL := <-windowRequests:
			url := makeURL()
			windowOpen.Store(true)
			err := openWindow(url)
			windowOpen.Store(false)
			if err == nil {
				continue
			}
			if !errors.Is(err, errNoWindow) {
				log.Printf("window not opened, using the browser: %v", err)
			}
			if err := desktop.OpenBrowser(url); err != nil {
				log.Printf("browser not opened: %v", err)
			}
		}
	}
}
