// Package update implements signed update manifests (ed25519) and the
// auto-update client for MyLab AutoPrint.
package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// PublicKeyB64 is the base64 ed25519 public key used to verify updates.
// It is embedded at build time with:
//
//	-ldflags "-X mylabautoprint/internal/update.PublicKeyB64=<base64>"
//
// It is intentionally NOT read from config.json.
var PublicKeyB64 = ""

// Manifest is the content of latest.json.
type Manifest struct {
	App            string                     `json:"app"`
	Version        string                     `json:"version"`
	Channel        string                     `json:"channel"`
	Released       string                     `json:"released"`
	Notes          string                     `json:"notes,omitempty"`
	InstallerName  string                     `json:"installer_name"`
	InstallerURL   string                     `json:"installer_url,omitempty"`
	InstallerSize  int64                      `json:"installer_size"`
	SHA256         string                     `json:"sha256"`
	ConfigOverride map[string]json.RawMessage `json:"config_override,omitempty"`
	LockedKeys     []string                   `json:"locked_keys,omitempty"`
	Signature      string                     `json:"signature"`
}

// SigningPayload returns the canonical bytes that are signed: the compact
// JSON encoding of the manifest with an empty signature.
func (m Manifest) SigningPayload() ([]byte, error) {
	m.Signature = ""
	return json.Marshal(m)
}

// Sign fills in the signature.
func (m *Manifest) Sign(priv ed25519.PrivateKey) error {
	p, err := m.SigningPayload()
	if err != nil {
		return err
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, p))
	return nil
}

// Verify checks the manifest signature against pub.
func (m Manifest) Verify(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("public key update tidak valid / tidak tertanam di aplikasi")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("signature manifest tidak valid")
	}
	p, err := m.SigningPayload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, p, sig) {
		return errors.New("signature manifest TIDAK COCOK (update ditolak)")
	}
	if len(m.SHA256) != 64 {
		return errors.New("sha256 manifest tidak valid")
	}
	return nil
}

// EmbeddedPublicKey decodes PublicKeyB64.
func EmbeddedPublicKey() (ed25519.PublicKey, error) {
	return DecodePublicKey(PublicKeyB64)
}

// DecodePublicKey decodes a base64 public key.
func DecodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("public key ed25519 tidak valid")
	}
	return ed25519.PublicKey(b), nil
}

// DecodePrivateKey decodes a base64 private key (64-byte key or 32-byte seed).
func DecodePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	switch len(b) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(b), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	}
	return nil, errors.New("private key ed25519 tidak valid")
}

// FileSHA256 returns hex sha256 and size of a file.
func FileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// CompareVersions returns -1, 0, 1 comparing dotted versions (leading "v"
// and pre-release suffixes after "-" are handled: 1.2.0-beta < 1.2.0).
func CompareVersions(a, b string) int {
	pa, sa := splitVer(a)
	pb, sb := splitVer(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case sa == sb:
		return 0
	case sa == "":
		return 1
	case sb == "":
		return -1
	case sa < sb:
		return -1
	}
	return 1
}

func splitVer(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	suffix := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		suffix = v[i+1:]
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out, suffix
}

// Describe returns a short human description.
func (m Manifest) Describe() string {
	return fmt.Sprintf("v%s (%s, %d bytes)", strings.TrimPrefix(m.Version, "v"), m.InstallerName, m.InstallerSize)
}
