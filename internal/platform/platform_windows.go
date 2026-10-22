//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// AppName is used for registry values, mutex and folders.
const AppName = "MyLabAutoPrint"

// DataDir returns %ProgramData%\MyLabAutoPrint if writable, otherwise
// %LOCALAPPDATA%\MyLabAutoPrint. MYLABAUTOPRINT_HOME overrides both.
func DataDir() string {
	if d := os.Getenv("MYLABAUTOPRINT_HOME"); d != "" {
		_ = os.MkdirAll(d, 0o755)
		return d
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		d := filepath.Join(pd, AppName)
		if writable(d) {
			return d
		}
	}
	d := filepath.Join(os.Getenv("LOCALAPPDATA"), AppName)
	_ = os.MkdirAll(d, 0o755)
	return d
}

// TempDir returns a per-user temp dir for print jobs.
func TempDir() string {
	d := filepath.Join(os.TempDir(), AppName)
	_ = os.MkdirAll(d, 0o755)
	return d
}

// SingleInstance acquires a global named mutex. ok=false if another instance
// is already running.
func SingleInstance() (release func(), ok bool) {
	name, _ := windows.UTF16PtrFromString(`Global\MyLabAutoPrint-SingleInstance`)
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			if h != 0 {
				windows.CloseHandle(h)
			}
			return func() {}, false
		}
		// e.g. access denied when the mutex was created by another user
		return func() {}, false
	}
	return func() { windows.ReleaseMutex(h); windows.CloseHandle(h) }, true
}

// SetAutostart enables/disables start with Windows for the current user.
func SetAutostart(enabled bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enabled {
		err := k.DeleteValue(AppName)
		if errors.Is(err, registry.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(AppName, `"`+exe+`"`)
}

// AutostartEnabled reports whether the HKCU Run value exists.
func AutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(AppName)
	return err == nil
}

func shellExecute(verb, file, args string) error {
	v, _ := windows.UTF16PtrFromString(verb)
	f, _ := windows.UTF16PtrFromString(file)
	var a *uint16
	if args != "" {
		a, _ = windows.UTF16PtrFromString(args)
	}
	return windows.ShellExecute(0, v, f, a, nil, windows.SW_SHOWNORMAL)
}

// Open opens a URL or file with its default handler.
func Open(target string) error { return shellExecute("open", target, "") }

// RunInstaller starts an NSIS installer silently (/S). ShellExecute is used
// so that the UAC prompt appears when the installer requires elevation.
func RunInstaller(path string) error { return shellExecute("open", path, "/S /UPDATE") }

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

// MessageBox shows a dialog. With yesNo it returns true when Yes is clicked.
func MessageBox(title, text string, yesNo bool) bool {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	flags := uintptr(0x40 | 0x10000 | 0x40000) // MB_ICONINFORMATION|MB_SETFOREGROUND|MB_TOPMOST
	if yesNo {
		flags = 0x4 | 0x20 | 0x10000 | 0x40000 // MB_YESNO|MB_ICONQUESTION
	}
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), flags)
	return r == 6 // IDYES
}
