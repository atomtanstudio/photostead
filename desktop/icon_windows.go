//go:build windows

package main

import (
	_ "embed"

	"fyne.io/systray"
)

//go:embed assets/photostead.ico
var trayIcon []byte

func setTrayIcon() { systray.SetIcon(trayIcon) }
