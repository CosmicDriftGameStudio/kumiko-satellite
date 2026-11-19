// Command kumiko-satellite pairs a machine with a Kumiko app and delivers files from one folder.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/api"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/config"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/filter"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/pairing"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/status"
	"github.com/CosmicDriftGameStudio/kumiko-satellite/internal/syncer"
)

// Set at build time: -ldflags "-X main.version=1.0.0".
var version = "dev"

const (
	scanEvery      = 30 * time.Second
	heartbeatEvery = 60 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "pair":
		err = pair(ctx, os.Args[2:])
	case "run":
		err = run(ctx, os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  kumiko-satellite pair --url <app-url> [--folder <path>]
  kumiko-satellite run [--folder <path>] [--status-addr 127.0.0.1:8765]
  kumiko-satellite version`)
}

func pair(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	appURL := fs.String("url", "", "app URL, e.g. https://app.example.com")
	folder := fs.String("folder", "", "folder to watch (can also be given to run)")
	_ = fs.Parse(args)
	if *appURL == "" {
		return errors.New("--url is required")
	}
	if *folder != "" {
		if err := filter.CheckFolder(*folder); err != nil {
			return err
		}
	}
	flow, err := pairing.New(*appURL)
	if err != nil {
		return err
	}
	auth, err := flow.Start(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Öffne %s\nGib den Code ein: %s\nWarte auf Bestätigung (gültig für %d s) ...\n",
		auth.VerificationURI, auth.UserCode, auth.ExpiresIn)
	token, err := flow.Poll(ctx, auth)
	if err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg := config.Config{URL: *appURL, Token: token, Folder: *folder}
	if old, err := config.Load(path); err == nil && cfg.Folder == "" {
		cfg.Folder = old.Folder
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Println("Gekoppelt, Zugang gespeichert in", path)
	return nil
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	folder := fs.String("folder", "", "folder to watch (default: from config)")
	addr := fs.String("status-addr", "127.0.0.1:8765", "loopback address of the status page")
	_ = fs.Parse(args)

	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if *folder == "" {
		*folder = cfg.Folder
	}
	if *folder == "" {
		return errors.New("no folder: pass --folder or set it with `pair --folder`")
	}
	if err := filter.CheckFolder(*folder); err != nil {
		return err
	}
	root, err := filepath.Abs(*folder)
	if err != nil {
		return err
	}
	if err := status.ValidateAddr(*addr); err != nil {
		return err
	}
	client, err := api.New(cfg.URL, cfg.Token)
	if err != nil {
		return err
	}
	statePath, err := config.StatePath()
	if err != nil {
		return err
	}
	engine := syncer.New(root, statePath, client)

	handler, err := status.Handler(engine, version, cfg.URL)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	fmt.Printf("Watching %s, status page: http://%s/\n", root, ln.Addr())
	go engine.RunHeartbeat(ctx, client, version, heartbeatEvery)
	engine.Run(ctx, scanEvery)
	return nil
}
