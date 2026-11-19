package main

import (
	"errors"
	"fmt"
	"net"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/desktop"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/session"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/status"
)

// signalRunning asks the instance recorded in the session file to show its
// window. Without a readable file nothing is sent: a running instance of
// another user cannot be reached, and must not be.
func signalRunning(sessionPath string) error {
	info, err := session.Read(sessionPath)
	if err != nil {
		return fmt.Errorf("no running Kumiko Satellite of your user found: %w", err)
	}
	if !status.SignalShow(info.Addr(), info.Secret) {
		return errors.New("the recorded Kumiko Satellite does not answer")
	}
	return nil
}

// listenWithSession listens on addr, or on a free loopback port when addr is
// taken (typically by another user's instance), and records port and a fresh
// secret for this user.
func listenWithSession(addr, sessionPath string) (net.Listener, session.Info, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, session.Info{}, err
	}
	secret, err := session.NewSecret()
	if err != nil {
		ln.Close()
		return nil, session.Info{}, err
	}
	info := session.Info{Port: ln.Addr().(*net.TCPAddr).Port, Secret: secret}
	if err := session.Write(sessionPath, info); err != nil {
		ln.Close()
		return nil, session.Info{}, err
	}
	return ln, info, nil
}

// openCommand opens the login link of the running instance in the browser.
func openCommand() error {
	path, err := session.Path()
	if err != nil {
		return err
	}
	info, err := session.Read(path)
	if err != nil {
		return fmt.Errorf("no running Kumiko Satellite of your user found: %w", err)
	}
	nonce, err := status.FetchNonce(info.Addr(), info.Secret)
	if err != nil {
		return fmt.Errorf("the running Kumiko Satellite did not give a login: %w", err)
	}
	return desktop.OpenBrowser(info.LoginURL(nonce))
}
