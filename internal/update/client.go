package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mylabautoprint/internal/config"
)

// State is the updater state exposed to the UI / API.
type State struct {
	CurrentVersion string    `json:"current_version"`
	Checking       bool      `json:"checking"`
	Installing     bool      `json:"installing"`
	LastCheck      time.Time `json:"last_check"`
	LastError      string    `json:"last_error,omitempty"`
	Available      bool      `json:"available"`
	Latest         *Manifest `json:"latest,omitempty"`
	KeyEmbedded    bool      `json:"key_embedded"`
}

// Hooks are platform actions supplied by main.
type Hooks struct {
	// RunInstaller starts the downloaded installer silently and must make the
	// app exit afterwards (the installer restarts it).
	RunInstaller func(path string) error
	// Busy reports whether print jobs are running (auto mode waits).
	Busy func() bool
	// OnState is called whenever the state changes.
	OnState func(State)
}

// Updater checks for and installs signed updates.
type Updater struct {
	cfg     *config.Store
	version string
	logger  *log.Logger
	dlDir   string
	hooks   Hooks
	http    *http.Client

	mu    sync.Mutex
	state State
	// assetURLs maps installer asset names to URLs from the last GitHub release.
	assetURLs map[string]string
	// manifestURL is the URL the manifest was fetched from.
	manifestURL string
}

// New creates an updater.
func New(cfg *config.Store, version string, logger *log.Logger, dlDir string, hooks Hooks) *Updater {
	u := &Updater{cfg: cfg, version: version, logger: logger, dlDir: dlDir, hooks: hooks,
		http: &http.Client{Timeout: 10 * time.Minute}}
	u.state.CurrentVersion = version
	_, err := EmbeddedPublicKey()
	u.state.KeyEmbedded = err == nil
	return u
}

// State returns a copy of the state.
func (u *Updater) State() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

func (u *Updater) setState(f func(*State)) {
	u.mu.Lock()
	f(&u.state)
	s := u.state
	u.mu.Unlock()
	if u.hooks.OnState != nil {
		u.hooks.OnState(s)
	}
}

// Loop runs periodic checks until ctx is done.
func (u *Updater) Loop(ctx context.Context) {
	select {
	case <-time.After(20 * time.Second):
	case <-ctx.Done():
		return
	}
	for {
		c := u.cfg.Get().Update
		if c.Enabled {
			m, avail, err := u.Check(ctx)
			if err != nil {
				u.logger.Printf("cek update gagal: %v", err)
			} else if avail && c.Mode == "auto" {
				for u.hooks.Busy != nil && u.hooks.Busy() {
					time.Sleep(5 * time.Second)
				}
				u.logger.Printf("mode auto: memasang update %s", m.Describe())
				if err := u.Install(ctx); err != nil {
					u.logger.Printf("auto update gagal: %v", err)
				}
			}
		}
		interval := time.Duration(c.CheckIntervalHours) * time.Hour
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return
		}
	}
}

func (u *Updater) get(ctx context.Context, rawURL string, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "MyLabAutoPrint/"+u.version)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// fetchManifest downloads latest.json from the configured source.
func (u *Updater) fetchManifest(ctx context.Context) (*Manifest, error) {
	c := u.cfg.Get().Update
	var manifestURL string
	assets := map[string]string{}
	if c.Source == "url" {
		if c.URL == "" {
			return nil, errors.New("update.url belum diisi")
		}
		manifestURL = c.URL
	} else {
		if c.Repo == "" || c.Repo == "OWNER/REPO" || !strings.Contains(c.Repo, "/") {
			return nil, errors.New("update.repo belum diisi (format OWNER/REPO)")
		}
		api := os.Getenv("MYLABAUTOPRINT_GITHUB_API")
		if api == "" {
			api = "https://api.github.com"
		}
		var rel ghRelease
		if c.Channel == "stable" {
			b, err := u.get(ctx, api+"/repos/"+c.Repo+"/releases/latest", "application/vnd.github+json")
			if err != nil {
				if os.Getenv("MYLABAUTOPRINT_GITHUB_API") != "" {
					return nil, err
				}
				// API limit (60 req/jam per IP, mis. banyak PC di belakang satu IP) atau API error:
				// fallback ke URL unduhan langsung, tidak memakai kuota API. Installer di-resolve
				// relatif terhadap URL ini dan tetap diverifikasi sha256 + tanda tangan.
				log.Printf("update: GitHub API gagal (%v), fallback ke releases/latest/download", err)
				rel = ghRelease{TagName: "latest", Assets: []ghAsset{{Name: "latest.json",
					URL: "https://github.com/" + c.Repo + "/releases/latest/download/latest.json"}}}
			} else if err := json.Unmarshal(b, &rel); err != nil {
				return nil, err
			}
		} else {
			b, err := u.get(ctx, api+"/repos/"+c.Repo+"/releases?per_page=15", "application/vnd.github+json")
			if err != nil {
				return nil, err
			}
			var rels []ghRelease
			if err := json.Unmarshal(b, &rels); err != nil {
				return nil, err
			}
			for _, r := range rels {
				if !r.Draft {
					rel = r
					break
				}
			}
		}
		for _, a := range rel.Assets {
			assets[a.Name] = a.URL
		}
		manifestURL = assets["latest.json"]
		if manifestURL == "" {
			return nil, fmt.Errorf("release %s tidak punya asset latest.json", rel.TagName)
		}
	}
	b, err := u.get(ctx, manifestURL, "")
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("latest.json tidak valid: %w", err)
	}
	pub, err := EmbeddedPublicKey()
	if err != nil {
		return nil, err
	}
	if err := m.Verify(pub); err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.assetURLs = assets
	u.manifestURL = manifestURL
	u.mu.Unlock()
	return &m, nil
}

// Check checks for a newer version.
func (u *Updater) Check(ctx context.Context) (*Manifest, bool, error) {
	u.setState(func(s *State) { s.Checking = true })
	m, err := u.fetchManifest(ctx)
	avail := err == nil && CompareVersions(m.Version, u.version) > 0
	u.setState(func(s *State) {
		s.Checking = false
		s.LastCheck = time.Now()
		if err != nil {
			s.LastError = err.Error()
			return
		}
		s.LastError = ""
		s.Available = avail
		s.Latest = m
	})
	return m, avail, err
}

func (u *Updater) installerURL(m *Manifest) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if m.InstallerURL != "" {
		if strings.HasPrefix(m.InstallerURL, "http://") || strings.HasPrefix(m.InstallerURL, "https://") {
			return m.InstallerURL, nil
		}
	}
	if a := u.assetURLs[m.InstallerName]; a != "" {
		return a, nil
	}
	if u.manifestURL != "" {
		base, err := url.Parse(u.manifestURL)
		if err == nil {
			ref := m.InstallerURL
			if ref == "" {
				ref = m.InstallerName
			}
			r, err := base.Parse(ref)
			if err == nil {
				return r.String(), nil
			}
		}
	}
	return "", errors.New("URL installer tidak ditemukan di manifest/release")
}

// Download fetches the installer of m and verifies its sha256 (the sha256 is
// covered by the manifest's ed25519 signature). Returns the local path.
func (u *Updater) Download(ctx context.Context, m *Manifest) (string, error) {
	src, err := u.installerURL(m)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(u.dlDir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(m.InstallerName)
	if name == "" || name == "." || name == "/" {
		name = "MyLabAutoPrint-Setup.exe"
	}
	dst := filepath.Join(u.dlDir, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "MyLabAutoPrint/"+u.version)
	resp, err := u.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download installer: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst + ".part")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 512<<20))
	f.Close()
	if err != nil {
		os.Remove(dst + ".part")
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, m.SHA256) {
		os.Remove(dst + ".part")
		return "", fmt.Errorf("sha256 installer tidak cocok (dapat %s, harus %s): update ditolak", got, m.SHA256)
	}
	os.Remove(dst)
	if err := os.Rename(dst+".part", dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Install re-fetches and verifies the latest manifest, downloads and verifies
// the installer, applies config overrides (with backup) and runs the installer.
func (u *Updater) Install(ctx context.Context) error {
	u.setState(func(s *State) { s.Installing = true })
	defer u.setState(func(s *State) { s.Installing = false })
	m, err := u.fetchManifest(ctx)
	if err != nil {
		return err
	}
	if CompareVersions(m.Version, u.version) <= 0 {
		return fmt.Errorf("sudah versi terbaru (%s)", u.version)
	}
	path, err := u.Download(ctx, m)
	if err != nil {
		u.setState(func(s *State) { s.LastError = err.Error() })
		return err
	}
	if len(m.ConfigOverride) > 0 || len(m.LockedKeys) > 0 {
		backup, err := u.cfg.ApplyOverrides(m.ConfigOverride, m.LockedKeys)
		if err != nil {
			return fmt.Errorf("config override gagal: %w", err)
		}
		u.logger.Printf("config override diterapkan (%d key, backup: %s)", len(m.ConfigOverride), backup)
	}
	u.logger.Printf("menjalankan installer %s (silent)", path)
	if u.hooks.RunInstaller == nil {
		return errors.New("installer tidak bisa dijalankan di platform ini")
	}
	return u.hooks.RunInstaller(path)
}
