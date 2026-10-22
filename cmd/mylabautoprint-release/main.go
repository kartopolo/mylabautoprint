// mylabautoprint-release: key generation, signing and verification of
// MyLab AutoPrint update manifests (latest.json).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mylabautoprint/internal/update"
)

func usage() {
	fmt.Fprint(os.Stderr, `mylabautoprint-release - alat rilis update MyLab AutoPrint

Perintah:
  keygen  -out DIR                       buat keypair ed25519 (private.key + public.key)
  pubkey  -key private.key               tampilkan public key (base64) dari private key
  sign    -key private.key -installer Setup.exe -version 1.2.0 [opsi]   buat latest.json bertanda tangan
          opsi: -out latest.json  -url URL_INSTALLER  -notes "catatan"  -channel stable
                -override key=JSON (bisa berulang, contoh -override 'update.repo="me/repo"')
                -lock key (bisa berulang, kunci setting supaya tidak bisa diubah klien)
                -notes-out release-notes.md
  verify  -pub public.key|BASE64 -manifest latest.json [-installer Setup.exe]

Private key juga bisa diberikan lewat env MYLABAUTOPRINT_SIGNING_KEY (base64) dengan -key env.
`)
	os.Exit(2)
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+f+"\n", a...)
	os.Exit(1)
}

func readKeyArg(arg, env string) string {
	if arg == "env" {
		v := os.Getenv(env)
		if v == "" {
			die("env %s kosong", env)
		}
		return v
	}
	if b, err := os.ReadFile(arg); err == nil {
		return strings.TrimSpace(string(b))
	}
	return arg
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ExitOnError)
		out := fs.String("out", "keys", "folder output")
		force := fs.Bool("force", false, "timpa key yang sudah ada")
		_ = fs.Parse(args)
		privPath := filepath.Join(*out, "private.key")
		if _, err := os.Stat(privPath); err == nil && !*force {
			die("%s sudah ada (pakai -force untuk menimpa — update lama tidak akan bisa diverifikasi lagi!)", privPath)
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			die("%v", err)
		}
		if err := os.MkdirAll(*out, 0o700); err != nil {
			die("%v", err)
		}
		if err := os.WriteFile(privPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
			die("%v", err)
		}
		if err := os.WriteFile(filepath.Join(*out, "public.key"), []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
			die("%v", err)
		}
		fmt.Println("Keypair dibuat di", *out, "- public key:", base64.StdEncoding.EncodeToString(pub))
	case "pubkey":
		fs := flag.NewFlagSet("pubkey", flag.ExitOnError)
		key := fs.String("key", "", "private key file / env")
		_ = fs.Parse(args)
		priv, err := update.DecodePrivateKey(readKeyArg(*key, "MYLABAUTOPRINT_SIGNING_KEY"))
		if err != nil {
			die("%v", err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ExitOnError)
		key := fs.String("key", "", "private key file, atau 'env' (MYLABAUTOPRINT_SIGNING_KEY)")
		inst := fs.String("installer", "", "file installer Setup.exe")
		ver := fs.String("version", "", "versi, mis. 1.2.0")
		out := fs.String("out", "latest.json", "file output manifest")
		url := fs.String("url", "", "URL download installer (kosong = cari asset dengan nama yang sama di release / folder latest.json)")
		notes := fs.String("notes", "", "catatan rilis")
		notesOut := fs.String("notes-out", "", "tulis release notes markdown ke file ini")
		channel := fs.String("channel", "stable", "channel")
		var overrides, locks multi
		fs.Var(&overrides, "override", "key=JSON override config klien (berulang)")
		fs.Var(&locks, "lock", "key yang dikunci (berulang)")
		_ = fs.Parse(args)
		if *key == "" || *inst == "" || *ver == "" {
			usage()
		}
		priv, err := update.DecodePrivateKey(readKeyArg(*key, "MYLABAUTOPRINT_SIGNING_KEY"))
		if err != nil {
			die("private key: %v", err)
		}
		sum, size, err := update.FileSHA256(*inst)
		if err != nil {
			die("%v", err)
		}
		m := update.Manifest{App: "mylabautoprint", Version: strings.TrimPrefix(*ver, "v"), Channel: *channel,
			Released: time.Now().UTC().Format(time.RFC3339), Notes: *notes, InstallerName: filepath.Base(*inst),
			InstallerURL: *url, InstallerSize: size, SHA256: sum, LockedKeys: locks}
		if len(overrides) > 0 {
			m.ConfigOverride = map[string]json.RawMessage{}
			for _, o := range overrides {
				k, v, ok := strings.Cut(o, "=")
				if !ok || !json.Valid([]byte(v)) {
					die("override %q harus format key=JSON, contoh update.repo=\"owner/repo\" atau port=8282", o)
				}
				m.ConfigOverride[strings.TrimSpace(k)] = json.RawMessage(v)
			}
		}
		if err := m.Sign(priv); err != nil {
			die("%v", err)
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			die("%v", err)
		}
		if err := m.Verify(priv.Public().(ed25519.PublicKey)); err != nil {
			die("self-verify gagal: %v", err)
		}
		fmt.Printf("OK: %s ditandatangani (v%s, sha256 %s, %d bytes)\n", *out, m.Version, sum, size)
		if *notesOut != "" {
			var nb strings.Builder
			fmt.Fprintf(&nb, "## MyLab AutoPrint v%s\n\n%s\n\n- Installer: `%s` (%d bytes)\n- SHA256: `%s`\n", m.Version, *notes, m.InstallerName, size, sum)
			if len(m.ConfigOverride) > 0 {
				nb.WriteString("- Config override:")
				for k, v := range m.ConfigOverride {
					fmt.Fprintf(&nb, " `%s=%s`", k, v)
				}
				nb.WriteString("\n")
			}
			if len(m.LockedKeys) > 0 {
				fmt.Fprintf(&nb, "- Dikunci: `%s`\n", strings.Join(m.LockedKeys, "`, `"))
			}
			if err := os.WriteFile(*notesOut, []byte(nb.String()), 0o644); err != nil {
				die("%v", err)
			}
		}
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		pubArg := fs.String("pub", "", "public key file atau base64")
		man := fs.String("manifest", "latest.json", "manifest")
		inst := fs.String("installer", "", "installer (opsional, cek sha256)")
		_ = fs.Parse(args)
		pub, err := update.DecodePublicKey(readKeyArg(*pubArg, ""))
		if err != nil {
			die("%v", err)
		}
		b, err := os.ReadFile(*man)
		if err != nil {
			die("%v", err)
		}
		var m update.Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			die("%v", err)
		}
		if err := m.Verify(pub); err != nil {
			die("%v", err)
		}
		fmt.Println("signature manifest VALID:", m.Describe())
		if *inst != "" {
			sum, _, err := update.FileSHA256(*inst)
			if err != nil {
				die("%v", err)
			}
			if !strings.EqualFold(sum, m.SHA256) {
				die("sha256 installer TIDAK COCOK: %s != %s", sum, m.SHA256)
			}
			fmt.Println("sha256 installer COCOK")
		}
	default:
		usage()
	}
}
