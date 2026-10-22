//go:build !windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// AppName is used for folders.
const AppName = "MyLabAutoPrint"

// DataDir returns the config/log directory (dev/test builds).
func DataDir() string {
	if d := os.Getenv("MYLABAUTOPRINT_HOME"); d != "" {
		_ = os.MkdirAll(d, 0o755)
		return d
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	d = filepath.Join(d, "mylabautoprint")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// TempDir returns a temp dir for print jobs.
func TempDir() string {
	d := filepath.Join(os.TempDir(), "mylabautoprint")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// SingleInstance uses a lock file (flock).
func SingleInstance() (func(), bool) {
	f, err := os.OpenFile(filepath.Join(DataDir(), "instance.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return func() {}, false
	}
	return func() { f.Close() }, true
}

var autostart = false

// SetAutostart is a no-op on non-Windows (stub).
func SetAutostart(enabled bool) error { autostart = enabled; return nil }

// AutostartEnabled (stub).
func AutostartEnabled() bool { return autostart }

// Open opens a URL/file with xdg-open if available.
func Open(target string) error { return exec.Command("xdg-open", target).Start() }

// RunInstaller is not supported outside Windows.
func RunInstaller(path string) error {
	return fmt.Errorf("stub: installer %s hanya bisa dijalankan di Windows", path)
}

// MessageBox prints to stdout (stub).
func MessageBox(title, text string, yesNo bool) bool {
	fmt.Printf("[%s] %s\n", title, text)
	return false
}
