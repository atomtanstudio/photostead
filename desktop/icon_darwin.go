//go:build darwin

package main

import (
	_ "embed"

	"fyne.io/systray"
)

//go:embed assets/tray-mac.png
var trayIcon []byte

// setTrayIcon: a template image, so macOS draws it light or dark with the menu bar.
func setTrayIcon() { systray.SetTemplateIcon(trayIcon, trayIcon) }
