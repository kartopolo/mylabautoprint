//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

const consoleLog = true

// runUI (non-Windows dev/test build): no tray, run until SIGINT/SIGTERM.
func runUI(app *App) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	app.Quit = func() { close(done) }
	app.Logger.Printf("mode headless (tanpa tray). Pengaturan: %s", app.SettingsURL())
	select {
	case <-sig:
	case <-done:
	}
}
