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
	shuttingDown   atomic.Bool
	windowCloser   atomic.Pointer[func()]
	windowRequests = make(chan loginLinks, 1)
	windowOpen     atomic.Bool
)

// loginLinks build fresh single-use logins when a window opens: the native
// window gets its URL inside this process, a browser only a file:// URL.
type loginLinks struct {
	window  func() string
	browser func() (string, error)
}

// requestWindow asks the main goroutine to open the window unless one is open.
func requestWindow(links loginLinks) {
	if windowOpen.Load() {
		return
	}
	select {
	case windowRequests <- links:
	default:
	}
}

// setWindowCloser registers how to close the open window from another
// goroutine; it fires at once when the program is already shutting down.
func setWindowCloser(closeFn func()) {
	windowCloser.Store(&closeFn)
	if shuttingDown.Load() {
		closeFn()
	}
}

func clearWindowCloser() { windowCloser.Store(nil) }

// closeWindows ends a window the main goroutine is blocked in, so that the
// program can exit after SIGTERM, and keeps later requests from opening one.
func closeWindows() {
	shuttingDown.Store(true)
	if f := windowCloser.Load(); f != nil {
		(*f)()
	}
}

// serveWindows runs on the main goroutine until the program is done. Closing a
// window does not stop the sync; a later start asks for a new window.
func serveWindows(done <-chan int) int {
	for {
		select {
		case code := <-done:
			return code
		case links := <-windowRequests:
			if shuttingDown.Load() {
				continue
			}
			windowOpen.Store(true)
			err := openWindow(links.window())
			windowOpen.Store(false)
			if err == nil {
				continue
			}
			if !errors.Is(err, errNoWindow) {
				log.Printf("window not opened, using the browser: %v", err)
			}
			url, err := links.browser()
			if err == nil {
				err = desktop.OpenBrowser(url)
			}
			if err != nil {
				log.Printf("browser not opened: %v", err)
			}
		}
	}
}
