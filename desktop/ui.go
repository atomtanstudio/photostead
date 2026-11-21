package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"fyne.io/systray"
)

// runTray shows the icon and its menu; it returns when the person quits.
func runTray(rt *Runtime) {
	// Told to stop (logging out, Activity Monitor, Task Manager): quit as if from the menu, so the
	// server and Postgres stop cleanly too.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		rt.Stop() // first: quitting the tray from here ends the process without its exit hook
		keepAwake(false)
		systray.Quit()
	}()
	systray.Run(func() { onReady(rt) }, func() { rt.Stop(); keepAwake(false) })
}

func onReady(rt *Runtime) {
	setTrayIcon()
	systray.SetTooltip("Photostead")

	status := systray.AddMenuItem("Photostead is starting…", "")
	status.Disable()
	open := systray.AddMenuItem("Open Photostead", "Photostead in your browser")
	pair := systray.AddMenuItem("Pair a Phone…", "Opens Photostead, where a phone pairs by scanning a code")
	systray.AddSeparator()
	lib := systray.AddMenuItem("Photo Library: "+filepath.Base(rt.cfg.Library), rt.cfg.Library)
	showLib := lib.AddSubMenuItem("Show in "+fileManagerName(), "")
	changeLib := lib.AddSubMenuItem("Choose Another Folder…", "Where uploads and phone backups go")
	awakeItem := systray.AddMenuItemCheckbox("Keep This Computer Awake", "So phones can back up at night (on power)", rt.cfg.KeepAwake)
	login := systray.AddMenuItemCheckbox("Open at Login", "Start Photostead when you log in", loginItemEnabled())
	logs := systray.AddMenuItem("Show Logs", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit Photostead", "Stops Photostead: phones can't back up until it's open again")

	keepAwake(rt.cfg.KeepAwake)

	var once sync.Once
	rt.OnChange(func(s State) {
		status.SetTitle(s.Text())
		if s.Phase == Running || s.Phase == Setup {
			open.Enable()
			pair.Enable()
		} else {
			open.Disable()
			pair.Disable()
		}
		if s.Phase == Setup { // the first time: the browser opens on making the first account
			once.Do(func() { openURL(rt.URL()) })
		}
	})
	go rt.Run()

	go func() {
		for {
			select {
			case <-open.ClickedCh:
				openURL(rt.URL())
			case <-pair.ClickedCh:
				openURL(rt.URL())
			case <-showLib.ClickedCh:
				revealFolder(rt.cfg.Library)
			case <-changeLib.ClickedCh:
				if p, ok := pickFolder("Where should Photostead keep uploads and phone backups?"); ok && p != rt.cfg.Library {
					rt.cfg.Library = p
					_ = rt.cfg.save(rt.paths)
					lib.SetTitle("Photo Library: " + filepath.Base(p))
					lib.SetTooltip(p)
					rt.Restart()
				}
			case <-awakeItem.ClickedCh:
				rt.cfg.KeepAwake = !rt.cfg.KeepAwake
				_ = rt.cfg.save(rt.paths)
				keepAwake(rt.cfg.KeepAwake)
				if rt.cfg.KeepAwake {
					awakeItem.Check()
				} else {
					awakeItem.Uncheck()
				}
			case <-login.ClickedCh:
				on := !loginItemEnabled()
				if err := setLoginItem(on); err == nil {
					if on {
						login.Check()
					} else {
						login.Uncheck()
					}
				}
			case <-logs.ClickedCh:
				revealFolder(rt.paths.Logs)
			case <-quit.ClickedCh:
				status.SetTitle("Photostead is stopping…")
				rt.Stop()
				keepAwake(false)
				systray.Quit()
				return
			}
		}
	}()
}
