//go:build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"mylabautoprint/internal/platform"
	"mylabautoprint/internal/update"
)

const consoleLog = false

//go:embed assets/tray.ico
var trayIcon []byte

//go:embed assets/tray_update.ico
var trayIconUpdate []byte

func runUI(app *App) {
	var once sync.Once
	app.Quit = func() { once.Do(systray.Quit) }
	systray.Run(func() { onReady(app) }, func() {})
}

func onReady(app *App) {
	systray.SetIcon(trayIcon)
	systray.SetTitle("MyLab AutoPrint")
	systray.SetTooltip("MyLab AutoPrint v" + Version + " - aktif")

	mSettings := systray.AddMenuItem("Pengaturan", "Buka halaman pengaturan")
	mStatus := systray.AddMenuItem("Status", "Lihat status")
	mLog := systray.AddMenuItem("Lihat Log", "Buka file log")
	systray.AddSeparator()
	mUpdate := systray.AddMenuItem("Cek Update", "Cek versi terbaru")
	mInstall := systray.AddMenuItem("Update tersedia", "Klik untuk download & pasang update")
	mInstall.Hide()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Keluar", "Tutup MyLab AutoPrint")

	app.onState = func(s update.State) {
		if s.Available && s.Latest != nil && !s.Installing {
			mInstall.SetTitle("Update tersedia v" + strings.TrimPrefix(s.Latest.Version, "v") + " - klik untuk pasang")
			mInstall.Show()
			systray.SetIcon(trayIconUpdate)
			systray.SetTooltip("MyLab AutoPrint v" + Version + " - update v" + s.Latest.Version + " tersedia")
		} else if s.Installing {
			mInstall.SetTitle("Memasang update…")
			mInstall.Disable()
		} else {
			mInstall.Hide()
			systray.SetIcon(trayIcon)
		}
	}
	systray.SetOnTapped(func() { _ = platform.Open(app.SettingsURL()) })

	install := func() {
		ctx, cancel := context.WithTimeout(app.Ctx, 15*time.Minute)
		defer cancel()
		if err := app.Upd.Install(ctx); err != nil {
			mInstall.Enable()
			platform.MessageBox("MyLab AutoPrint - Update", "Update gagal:\n"+err.Error(), false)
		}
	}

	go func() {
		for {
			select {
			case <-mSettings.ClickedCh:
				_ = platform.Open(app.SettingsURL())
			case <-mStatus.ClickedCh:
				go showStatus(app)
			case <-mLog.ClickedCh:
				_ = platform.Open(app.LogPath)
			case <-mUpdate.ClickedCh:
				go func() {
					ctx, cancel := context.WithTimeout(app.Ctx, time.Minute)
					defer cancel()
					m, avail, err := app.Upd.Check(ctx)
					switch {
					case err != nil:
						platform.MessageBox("MyLab AutoPrint - Update", "Cek update gagal:\n"+err.Error(), false)
					case avail:
						if platform.MessageBox("MyLab AutoPrint - Update",
							fmt.Sprintf("Versi baru tersedia: v%s\n%s\n\nPasang sekarang?", m.Version, m.Notes), true) {
							install()
						}
					default:
						platform.MessageBox("MyLab AutoPrint - Update", "Sudah versi terbaru (v"+Version+").", false)
					}
				}()
			case <-mInstall.ClickedCh:
				go install()
			case <-mQuit.ClickedCh:
				app.Quit()
				return
			}
		}
	}()
}

func showStatus(app *App) {
	ps, def, err := app.Mgr.Backend().ListPrinters()
	var b strings.Builder
	fmt.Fprintf(&b, "MyLab AutoPrint v%s\n\nAPI: http://127.0.0.1:%d\n", Version, app.Srv.Port())
	if err != nil {
		fmt.Fprintf(&b, "Printer: error %v\n", err)
	} else {
		fmt.Fprintf(&b, "Printer terpasang: %d\nPrinter default Windows: %s\n", len(ps), def)
	}
	jobs := app.Mgr.Jobs()
	fmt.Fprintf(&b, "Job di riwayat: %d\n", len(jobs))
	if len(jobs) > 0 {
		j := jobs[0]
		fmt.Fprintf(&b, "Job terakhir: %s %s -> %s (%s)\n", j.Created.Format("15:04:05"), j.Type, j.Printer, j.Status)
	}
	u := app.Upd.State()
	if u.Available && u.Latest != nil {
		fmt.Fprintf(&b, "\nUpdate tersedia: v%s\n", u.Latest.Version)
	}
	fmt.Fprintf(&b, "\nConfig: %s\nLog: %s", app.Cfg.Path(), app.LogPath)
	platform.MessageBox("MyLab AutoPrint - Status", b.String(), false)
}
