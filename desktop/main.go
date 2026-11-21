// Photostead on a Mac or a Windows PC, without Docker: this launcher starts the bundled Postgres and
// the Photostead server (which starts its AI worker itself), and shows a menu-bar (Mac) or tray
// (Windows) icon to open Photostead, pair a phone, choose where originals go, and quit. Everything
// it runs comes from its own folder ("runtime": Postgres, the server, ffmpeg, exiftool, Python and
// the web app); nothing is installed elsewhere. See docs/plans/desktop-app-plan.md.
//
//	Photostead                      the icon, as a person runs it
//	Photostead -headless            no icon: runs until <data>/stop appears (tests, CI)
//	  -data DIR -library DIR -port N -runtime DIR   where things are, instead of the defaults
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	headless := flag.Bool("headless", false, "no icon: run until the file <data>/stop appears (tests)")
	dataDir := flag.String("data", os.Getenv("PHOTOSTEAD_DESKTOP_DATA"), "where the database, settings and logs live")
	library := flag.String("library", os.Getenv("PHOTOSTEAD_DESKTOP_LIBRARY"), "where originals go")
	port := flag.Int("port", 0, "the web app's port (4800 unless set before)")
	runtimeDir := flag.String("runtime", os.Getenv("PHOTOSTEAD_RUNTIME"), "the bundled programs")
	flag.Parse()

	paths := defaultPaths()
	if *dataDir != "" {
		paths.Data = *dataDir
	}
	if *runtimeDir != "" {
		paths.Runtime = *runtimeDir
	}
	// Absolute, so a relative -runtime or -data still means the same once the server runs elsewhere.
	if p, err := filepath.Abs(paths.Runtime); err == nil {
		paths.Runtime = p
	}
	if p, err := filepath.Abs(paths.Data); err == nil {
		paths.Data = p
	}
	paths.Logs = filepath.Join(paths.Data, "logs")
	for _, d := range []string{paths.Data, paths.Logs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fail(err)
		}
	}
	logFile, err := os.OpenFile(filepath.Join(paths.Logs, "launcher.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fail(err)
	}
	log := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logFile), nil))

	unlock, err := lock(filepath.Join(paths.Data, "launcher.lock"))
	if err != nil { // already running: a second click opens the one that is
		log.Info("already running", "err", err)
		if cfg, err := loadConfig(paths); err == nil && !*headless {
			openURL(fmt.Sprintf("http://localhost:%d/", cfg.Port))
		}
		return
	}
	defer unlock()

	cfg, err := loadConfig(paths)
	if err != nil {
		fail(err)
	}
	if *library != "" {
		cfg.Library = *library
	}
	if p, err := filepath.Abs(cfg.Library); err == nil {
		cfg.Library = p
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if err := cfg.save(paths); err != nil {
		fail(err)
	}
	log.Info("starting", "runtime", paths.Runtime, "data", paths.Data, "library", cfg.Library, "port", cfg.Port)

	rt := newRuntime(paths, cfg, log)
	if *headless {
		go rt.Run()
		waitForStop(paths.Data)
		rt.Stop()
		return
	}
	runTray(rt)
}

// waitForStop returns when <data>/stop appears (removing it), or on an interrupt.
func waitForStop(data string) {
	stopFile := filepath.Join(data, "stop")
	_ = os.Remove(stopFile)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-sig:
			return
		case <-tick.C:
			if _, err := os.Stat(stopFile); err == nil {
				_ = os.Remove(stopFile)
				return
			}
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "photostead:", err)
	os.Exit(1)
}
