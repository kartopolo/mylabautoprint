// Package config handles loading, merging, saving and overriding the
// MyLab AutoPrint configuration (config.json).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Printers holds the default printer per job type. Empty = fall back to
// "default", then to the Windows default printer.
type Printers struct {
	Default string `json:"default"`
	Raw     string `json:"raw"`
	PDF     string `json:"pdf"`
	Image   string `json:"image"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

// PrintDefaults are used when a print request does not specify them.
type PrintDefaults struct {
	Paper       string `json:"paper"`       // "", A4, A5, A3, Letter, Legal
	Orientation string `json:"orientation"` // portrait | landscape
	Copies      int    `json:"copies"`
	PDFScale    string `json:"pdf_scale"` // fit | shrink | noscale
}

// Update configures the auto-updater.
type Update struct {
	Enabled            bool   `json:"enabled"`
	Mode               string `json:"mode"`   // notify | auto
	Source             string `json:"source"` // github | url
	Repo               string `json:"repo"`   // OWNER/REPO (source=github)
	URL                string `json:"url"`    // https://.../latest.json (source=url)
	Channel            string `json:"channel"`
	CheckIntervalHours int    `json:"check_interval_hours"`
}

// Config is the full application configuration.
type Config struct {
	// ConfigVersion is used for one-time migrations of old config files.
	ConfigVersion  int                        `json:"config_version"`
	Port           int                        `json:"port"`
	AllowedOrigins []string                   `json:"allowed_origins"`
	APIKey         string                     `json:"api_key"`
	Autostart      bool                       `json:"autostart"`
	Printers       Printers                   `json:"printers"`
	Defaults       PrintDefaults              `json:"defaults"`
	HistorySize    int                        `json:"history_size"`
	Update         Update                     `json:"update"`
	Locked         map[string]json.RawMessage `json:"locked,omitempty"`
}

// DefaultPort is the default local API port. It is deliberately uncommon and
// below the Windows dynamic/ephemeral range (49152-65535).
const DefaultPort = 47819

// LegacyPort was the default port of v1.0.0.
const LegacyPort = 8181

// CurrentConfigVersion is bumped whenever a migration is added.
const CurrentConfigVersion = 2

// Defaults returns the built-in default configuration.
func Defaults() Config {
	return Config{
		Port:           DefaultPort,
		AllowedOrigins: []string{"*"},
		APIKey:         "",
		Autostart:      true,
		Defaults:       PrintDefaults{Orientation: "portrait", Copies: 1, PDFScale: "fit"},
		HistorySize:    50,
		Update: Update{
			Enabled:            true,
			Mode:               "notify",
			Source:             "github",
			Repo:               "kartopolo/mylabautoprint-releases",
			Channel:            "stable",
			CheckIntervalHours: 6,
		},
	}
}

// Store is a thread-safe config holder bound to a file.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// Load reads config from path. Built-in defaults are applied first, then an
// optional "defaults.json" shipped next to the executable (developer
// defaults), then the user's config.json. Missing keys therefore always get
// default values, and the merged result is written back.
func Load(path, shippedDefaults string) (*Store, error) {
	cfg := Defaults()
	if shippedDefaults != "" {
		if b, err := os.ReadFile(shippedDefaults); err == nil {
			if err := json.Unmarshal(stripBOM(b), &cfg); err != nil {
				return nil, fmt.Errorf("defaults.json: %w", err)
			}
		}
	}
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(stripBOM(b), &cfg); err != nil {
			// keep a copy of the broken file and continue with defaults
			_ = os.WriteFile(path+".broken-"+time.Now().Format("20060102-150405"), b, 0o644)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	migrate(&cfg)
	sanitize(&cfg)
	if err := enforceLocked(&cfg); err != nil {
		return nil, err
	}
	s.cfg = cfg
	return s, s.saveLocked()
}

// migrate upgrades configs written by older versions.
func migrate(c *Config) {
	if c.ConfigVersion < 2 {
		// v1.0.0 used port 8181 by default; move untouched configs to the new default.
		if c.Port == LegacyPort {
			c.Port = DefaultPort
		}
	}
	c.ConfigVersion = CurrentConfigVersion
}

func stripBOM(b []byte) []byte { return []byte(strings.TrimPrefix(string(b), "\ufeff")) }

func sanitize(c *Config) {
	d := Defaults()
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = d.Port
	}
	if c.Defaults.Copies < 1 {
		c.Defaults.Copies = 1
	}
	if c.Defaults.Copies > 99 {
		c.Defaults.Copies = 99
	}
	if c.Defaults.Orientation != "landscape" {
		c.Defaults.Orientation = "portrait"
	}
	switch c.Defaults.PDFScale {
	case "fit", "shrink", "noscale":
	default:
		c.Defaults.PDFScale = "fit"
	}
	if c.HistorySize < 1 || c.HistorySize > 1000 {
		c.HistorySize = d.HistorySize
	}
	if c.Update.Mode != "auto" {
		c.Update.Mode = "notify"
	}
	if c.Update.Source != "url" {
		c.Update.Source = "github"
	}
	if c.Update.CheckIntervalHours < 1 {
		c.Update.CheckIntervalHours = d.Update.CheckIntervalHours
	}
	if c.Update.Channel == "" {
		c.Update.Channel = "stable"
	}
	if c.AllowedOrigins == nil {
		c.AllowedOrigins = []string{}
	}
}

// Path returns the config file path.
func (s *Store) Path() string { return s.path }

// Get returns a copy of the current config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.AllowedOrigins = append([]string(nil), s.cfg.AllowedOrigins...)
	return c
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Patch deep-merges a partial JSON object (e.g. {"update":{"mode":"auto"}})
// into the config. Locked keys cannot be changed by a patch; they are listed
// in the returned slice if the patch tried to change them.
func (s *Store) Patch(patch []byte) (ignored []string, err error) {
	var p map[string]any
	if err := json.Unmarshal(patch, &p); err != nil {
		return nil, fmt.Errorf("JSON tidak valid: %w", err)
	}
	delete(p, "locked")
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := toMap(s.cfg)
	if err != nil {
		return nil, err
	}
	deepMerge(cur, p)
	var next Config
	if err := fromMap(cur, &next); err != nil {
		return nil, err
	}
	next.Locked = s.cfg.Locked
	for k := range next.Locked {
		if hasPath(p, k) {
			ignored = append(ignored, k)
		}
	}
	sanitize(&next)
	if err := enforceLocked(&next); err != nil {
		return nil, err
	}
	s.cfg = next
	return ignored, s.saveLocked()
}

// ApplyOverrides applies developer overrides (dot-path keys -> JSON values),
// as delivered by a signed update manifest. The current config file is backed
// up first. Keys in lock are additionally locked against client changes.
// Returns the backup path.
func (s *Store) ApplyOverrides(over map[string]json.RawMessage, lock []string) (string, error) {
	if len(over) == 0 && len(lock) == 0 {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	backup := ""
	if b, err := os.ReadFile(s.path); err == nil {
		backup = s.path + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, b, 0o644); err != nil {
			return "", fmt.Errorf("backup config gagal: %w", err)
		}
	}
	cur, err := toMap(s.cfg)
	if err != nil {
		return backup, err
	}
	keys := make([]string, 0, len(over))
	for k := range over {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var v any
		if err := json.Unmarshal(over[k], &v); err != nil {
			return backup, fmt.Errorf("override %s: %w", k, err)
		}
		setPath(cur, k, v)
	}
	var next Config
	if err := fromMap(cur, &next); err != nil {
		return backup, err
	}
	next.Locked = map[string]json.RawMessage{}
	for k, v := range s.cfg.Locked {
		next.Locked[k] = v
	}
	for _, k := range lock {
		if v, ok := over[k]; ok {
			next.Locked[k] = v
		} else if v, ok := getPath(cur, k); ok {
			raw, _ := json.Marshal(v)
			next.Locked[k] = raw
		}
	}
	if len(next.Locked) == 0 {
		next.Locked = nil
	}
	sanitize(&next)
	if err := enforceLocked(&next); err != nil {
		return backup, err
	}
	s.cfg = next
	return backup, s.saveLocked()
}

// IsLocked reports whether a dot-path key is locked.
func (s *Store) IsLocked(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.cfg.Locked[key]
	return ok
}

func enforceLocked(c *Config) error {
	if len(c.Locked) == 0 {
		return nil
	}
	m, err := toMap(*c)
	if err != nil {
		return err
	}
	for k, raw := range c.Locked {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			setPath(m, k, v)
		}
	}
	locked := c.Locked
	var next Config
	if err := fromMap(m, &next); err != nil {
		return err
	}
	next.Locked = locked
	*c = next
	return nil
}

func toMap(c Config) (map[string]any, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	return m, json.Unmarshal(b, &m)
}

func fromMap(m map[string]any, c *Config) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	*c = Defaults()
	c.AllowedOrigins = nil
	return json.Unmarshal(b, c)
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

func setPath(m map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

func getPath(m map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func hasPath(m map[string]any, path string) bool {
	_, ok := getPath(m, path)
	return ok
}
