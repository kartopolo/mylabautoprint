# MyLab AutoPrint

Aplikasi print otomatis (silent print) yang ringan untuk Windows, sebagai pengganti QZ Tray.
Web app kamu mengirim perintah print ke `http://127.0.0.1:47819` (HTTP atau WebSocket), lalu
MyLab AutoPrint langsung mencetak ke printer klien **tanpa dialog print**.

- Satu file `mylabautoprint.exe` (Go, ±8,6 MB), tanpa Java / .NET / runtime lain.
- RAM idle sekitar 8–15 MB (diukur di build Linux uji, lihat bagian *Catatan RAM*).
- Ikon di tray Windows + halaman pengaturan di browser (bahasa Indonesia).
- Jenis print: **raw** (ESC/POS, ZPL, EPL), **PDF**, **gambar** (PNG/JPG/GIF), **teks**, **HTML** (best-effort).
- Ke printer apa pun yang terpasang di Windows + printer jaringan langsung `tcp://IP:9100`.
- Auto-update bertanda tangan digital (ed25519) dari GitHub Releases atau domain sendiri.

---

## Daftar isi
1. [Instalasi untuk klien](#1-instalasi-untuk-klien)
2. [Pakai dari web app (API)](#2-pakai-dari-web-app-api)
3. [Pengaturan (config.json)](#3-pengaturan-configjson)
4. [Merilis update](#4-merilis-update)
5. [Override config dari developer](#5-override-config-dari-developer)
6. [Build dari source](#6-build-dari-source)
7. [Troubleshooting](#7-troubleshooting)
8. [Keamanan](#8-keamanan)
9. [Batasan & catatan](#9-batasan--catatan)

---

## 1. Instalasi untuk klien

1. Download `MyLabAutoPrint-Setup-x.y.z.exe`.
2. Double-click. Kalau muncul layar biru **"Windows protected your PC"** (SmartScreen):
   klik **More info** (Info selengkapnya) → **Run anyway** (Tetap jalankan).
   Ini muncul karena installer belum ditandatangani sertifikat *code signing*, bukan karena virus.
3. Klik **Ya** di jendela UAC (izin administrator), lalu **Next → Install → Finish**.
4. Selesai. Ikon printer biru muncul di tray (pojok kanan bawah, dekat jam; kadang di dalam panah `^`).
   Aplikasi otomatis jalan setiap Windows nyala.

Menu ikon tray (klik kanan):

| Menu | Fungsi |
|---|---|
| **Pengaturan** | Membuka `http://127.0.0.1:47819/` di browser (pilih printer, tes print, dsb.) |
| **Status** | Versi, port, jumlah printer, job terakhir |
| **Lihat Log** | Membuka file log di Notepad |
| **Cek Update** | Cek versi terbaru sekarang |
| **Update tersedia vX** | Muncul jika ada update; klik → download, verifikasi, pasang, restart otomatis |
| **Keluar** | Menutup aplikasi |

Klik kiri ikon = buka Pengaturan.

**Uninstall:** Settings → Apps → *MyLab AutoPrint* → Uninstall (atau Start Menu → MyLab AutoPrint → Uninstall).

Lokasi file:
- Program: `C:\Program Files\MyLabAutoPrint\` (`mylabautoprint.exe`, `SumatraPDF.exe`, `defaults.json`)
- Config & log: `C:\ProgramData\MyLabAutoPrint\config.json` dan `...\logs\mylabautoprint.log`
  (jika folder itu tidak bisa ditulis, otomatis pakai `%LOCALAPPDATA%\MyLabAutoPrint\`)

---

## 2. Pakai dari web app (API)

Server hanya mendengarkan di `127.0.0.1` (tidak bisa diakses dari komputer lain).
Port default **47819** (sengaja port yang jarang dipakai, di bawah rentang port dinamis Windows 49152–65535).
Versi 1.0.0 memakai 8181; saat update ke 1.0.1+ config lama yang masih 8181 otomatis dipindah ke 47819.

**Bagaimana web app menemukan port?** `mylabautoprint.js` tanpa opsi `port` akan mencoba berurutan
`GET http://127.0.0.1:<port>/status` di port **47819** lalu **8181** (legacy) dan memakai yang pertama menjawab.
Jika koneksi putus (mis. aplikasi baru di-update dan pindah port), port dicari ulang otomatis.
- Port custom di klien: `new MyLabAutoPrint({ port: 50001 })` (hanya port itu), atau
  `new MyLabAutoPrint({ ports: [50001, 47819, 8181] })` (dicoba berurutan).
- Tanpa library: panggil `/status` di port-port tersebut sendiri; respons berisi `"app":"MyLab AutoPrint"` dan `"port"`.

### 2.1 Cara termudah: `mylabautoprint.js`

```html
<script src="mylabautoprint.js"></script>
<script>
  const ap = new MyLabAutoPrint({ apiKey: "" });   // port dideteksi otomatis (47819 → 8181); apiKey jika di-set
  ap.onStatus(s => console.log("AutoPrint:", s));              // connecting | connected | disconnected
  ap.onJob(job => console.log(job.id, job.status, job.error)); // queued | printing | done | error

  async function cetakStruk() {
    if (!(await ap.isAvailable())) { alert("MyLab AutoPrint belum terpasang / belum jalan"); return; }
    await ap.connect();                       // WebSocket (opsional, auto-reconnect). Tanpa ini pakai HTTP.
    const { printers, default: def } = await ap.listPrinters();
    await ap.printRaw(MyLabAutoPrint.escpos("TOKO MYLAB\nTotal: 20.000\n"), { printer: "POS-80" });
    await ap.printPDF("https://app.domainkamu.com/invoice/123.pdf", { printer: "HP LaserJet", paper: "A4" });
    await ap.printImage(fileInput.files[0]);   // Blob/File/Uint8Array/base64
    await ap.printText("Halo\nDunia", { copies: 2 });
    await ap.printHTML("<h1>Label</h1>");     // best-effort, lihat batasan
  }
</script>
```

`print()` / `printXxx()` mengembalikan Promise yang selesai (resolve) setelah job **done**,
atau gagal (reject) kalau job **error**. Pakai `ap.print(req, { wait: false })` untuk tidak menunggu.

Demo lengkap: buka `test.html` (bisa langsung dari file, atau taruh di web server kamu).

### 2.2 HTTP REST

| Method | Path | Keterangan |
|---|---|---|
| GET | `/status` | Versi, port, uptime, status update, pemakaian memori Go |
| GET | `/printers` | `{default, printers:[{name, default, network, server}]}` |
| POST | `/print` | Kirim job print (lihat di bawah) |
| GET | `/jobs` | Riwayat job (terbaru dulu, maks `history_size`) |
| GET | `/jobs/{id}` | Status satu job |
| GET | `/config` | Config lengkap + `locked_keys` |
| PUT/POST | `/config` | Ubah sebagian config (deep-merge), mis. `{"printers":{"raw":"POS-80"}}` |
| POST | `/test-print` | `{type:"text"|"raw"|"image"|"pdf", printer?}` |
| GET | `/update` | Status updater |
| POST | `/update/check` | Cek update sekarang |
| POST | `/update/install` | Pasang update (hanya dari halaman Pengaturan lokal) |
| GET | `/log?lines=200` | Baris terakhir log |
| GET | `/ws` | WebSocket |

**POST /print** – body JSON:

```json
{
  "type": "raw | pdf | image | text | html",
  "printer": "POS-80",            // opsional; kosong = default per tipe (Pengaturan) → default Windows
  "data": "<base64>",             // salah satu dari data / text / url
  "text": "teks biasa",           // untuk raw/text/html (string UTF-8)
  "url": "https://.../file.pdf",  // file diunduh oleh aplikasi; untuk html, URL dirender langsung
  "copies": 1,
  "paper": "A4",                  // A4, A5, A3, Letter, Legal, F4 (GDI & PDF)
  "orientation": "portrait",      // portrait | landscape
  "jobName": "Struk #123",
  "scale": "fit",                 // image: fit|width|noscale ; pdf: fit|shrink|noscale
  "pages": "1-2",                 // pdf
  "textMode": "gdi",              // text: gdi (font Consolas) | raw (kirim apa adanya)
  "fontSize": 10,                 // text gdi, dalam point
  "async": false                  // true = langsung balas status queued (cek via /jobs/{id} atau WS)
}
```

Balasan (sinkron): `{"ok":true,"job":{"id":"...","status":"done",...}}`; jika gagal HTTP 400/500 + `"error"`.

Printer jaringan langsung (raw/text): `"printer": "tcp://192.168.1.50:9100"` (port default 9100).

> Untuk data biner ESC/POS (byte > 127, mis. gambar/logo atau codepage), kirim lewat `data` (base64), bukan `text`
> — `text` dikirim sebagai UTF-8.

Contoh curl:

```bash
curl -X POST http://127.0.0.1:47819/print -H "Content-Type: application/json" \
  -d '{"type":"pdf","url":"https://contoh.com/a.pdf","printer":"HP LaserJet","copies":2}'
```

### 2.3 WebSocket `ws://127.0.0.1:47819/ws`

API key (jika ada): `ws://127.0.0.1:47819/ws?apiKey=KEY`.

Kirim: `{"id":"1","action":"print","params":{...sama seperti POST /print...}}`
Action lain: `printers`, `status`, `jobs`, `ping`.

Diterima:
- `{"type":"hello","version":"1.0.0"}` saat konek
- `{"type":"response","id":"1","ok":true,"result":{...}}`
- `{"type":"job","job":{"id":"...","status":"queued|printing|done|error","error":"..."}}` – real-time
- `{"type":"printers","default":"...","printers":[...]}` – saat konek & tiap daftar printer berubah (dicek tiap 20 detik)
- `{"type":"update","update":{...}}` – status update

### 2.4 Web app HTTPS → localhost

Chrome/Edge mengizinkan halaman `https://` memanggil `http://127.0.0.1` (localhost dianggap aman).
MyLab AutoPrint sudah mengirim header CORS dan `Access-Control-Allow-Private-Network: true`
(Private Network Access). Yang perlu kamu lakukan: masukkan origin web app kamu di *Website yang diizinkan*
(atau biarkan `*`). Firefox & Safari juga bisa, tapi Safari kadang memblokir *mixed content* – pakai Chrome/Edge di klien.

---

## 3. Pengaturan (config.json)

Urutan prioritas: **bawaan aplikasi** < `defaults.json` (di folder program, dari developer/installer) < `config.json` klien
< **locked** (dikunci developer lewat update). Key baru di versi baru otomatis ditambahkan dengan nilai default.

```json
{
  "config_version": 2,
  "port": 47819,
  "allowed_origins": ["*"],
  "api_key": "",
  "autostart": true,
  "printers": { "default": "", "raw": "", "pdf": "", "image": "", "text": "", "html": "" },
  "defaults": { "paper": "", "orientation": "portrait", "copies": 1, "pdf_scale": "fit" },
  "history_size": 50,
  "update": {
    "enabled": true,
    "mode": "notify",
    "source": "github",
    "repo": "kartopolo/mylabautoprint-releases",
    "url": "",
    "channel": "stable",
    "check_interval_hours": 6
  },
  "locked": { }
}
```

| Key | Arti |
|---|---|
| `port` | Port API & pengaturan, default 47819 (ganti di Pengaturan → otomatis pindah tanpa restart). Kalau diganti ke port selain 47819/8181, web app harus diberi `port`/`ports` yang sama |
| `config_version` | Dipakai untuk migrasi otomatis config lama (jangan diubah) |
| `allowed_origins` | Website yang boleh print. `*` = semua; `https://app.domainkamu.com`; wildcard `https://*.domainkamu.com`; `null` = file lokal |
| `api_key` | Jika diisi, request dari website wajib kirim header `X-API-Key` (atau `?apiKey=` untuk WS) |
| `autostart` | Jalan saat Windows nyala (HKCU\...\Run) |
| `printers.*` | Printer default per tipe job; kosong = `printers.default` → default Windows |
| `defaults.*` | Kertas, orientasi, salinan, skala PDF default |
| `update.mode` | `notify` = tampilkan "Update tersedia", klien klik; `auto` = pasang otomatis saat tidak ada job |
| `update.source` | `github` (pakai `repo`) atau `url` (pakai `url` ke `latest.json`) |
| `update.channel` | `stable` = release terbaru; selain itu (`beta`) = termasuk pre-release |
| `locked` | Diisi otomatis oleh update dari developer; jangan diedit manual |

**Untuk developer:** edit `installer/defaults.json` sebelum build supaya semua instalasi baru langsung memakai
setting kamu (mis. `allowed_origins` domain kamu, `api_key`).

---

## 4. Merilis update

Public key untuk verifikasi **tertanam di dalam exe** (`release/public.key` saat build). Private key ada di
`keys/private.key` (lihat `keys/README.md`). Klien hanya mau memasang update yang `latest.json`-nya
ditandatangani private key tersebut **dan** sha256 installer-nya cocok.

Isi `latest.json`:

```json
{
  "app": "mylabautoprint",
  "version": "1.0.1",
  "channel": "stable",
  "released": "2026-10-22T15:00:00Z",
  "notes": "Perbaikan bug",
  "installer_name": "MyLabAutoPrint-Setup-1.0.1.exe",
  "installer_url": "",
  "installer_size": 11218464,
  "sha256": "…",
  "config_override": { "update.check_interval_hours": 12 },
  "locked_keys": ["update.check_interval_hours"],
  "signature": "…base64 ed25519…"
}
```

`installer_url` kosong = cari asset dengan nama `installer_name` di release GitHub yang sama, atau
file bernama itu di folder yang sama dengan `latest.json` (mode URL).

### 4.1 Otomatis lewat GitHub Actions (disarankan)

Struktur repo:
- **`kartopolo/mylabautoprint`** (private) – source code ini, berisi `.github/workflows/release.yml`.
- **`kartopolo/mylabautoprint-releases`** (public) – hanya berisi Releases (installer + `latest.json`).
  Harus public supaya klien bisa download tanpa token.

Setup sekali (atau jalankan `./setup-github.sh` yang melakukan langkah 1–4):
1. Buat kedua repo.
2. Secret di repo source: `MYLABAUTOPRINT_SIGNING_KEY` = isi `keys/private.key`
   (`gh secret set MYLABAUTOPRINT_SIGNING_KEY --repo kartopolo/mylabautoprint < keys/private.key`).
3. **Langkah manual:** buat *fine-grained personal access token* di
   https://github.com/settings/personal-access-tokens/new → *Only select repositories*: `mylabautoprint-releases`
   → *Permissions → Contents: Read and write*. Simpan sebagai secret `RELEASES_TOKEN`
   (`gh secret set RELEASES_TOKEN --repo kartopolo/mylabautoprint`). `GITHUB_TOKEN` bawaan Actions tidak bisa
   membuat release di repo lain, jadi token ini wajib.
4. Push source ke repo private.

Setiap rilis:
```bash
git tag -a v1.0.1 -m "Perbaikan bug printer thermal"   # pesan tag = catatan rilis
git push origin v1.0.1
```
Workflow akan: build exe + installer → tanda tangan `latest.json` → cek cocok dengan `release/public.key`
→ publish ke `kartopolo/mylabautoprint-releases` (tag dengan `-`, mis. `v1.1.0-beta1`, jadi pre-release).
Hasil build juga tersimpan sebagai *artifact* di tab Actions.

### 4.2 Manual dari komputer sendiri (cadangan)

```bash
cd source
./release.sh 1.0.1 "Perbaikan bug"              # build + sign → release/out/v1.0.1/
./release.sh 1.0.1 "Perbaikan bug" --publish    # + upload ke GitHub Releases via gh
```

Atau langkah demi langkah dengan alat rilis (ada versi Windows `mylabautoprint-release.exe`):
```bash
mylabautoprint-release sign -key keys/private.key -installer MyLabAutoPrint-Setup-1.0.1.exe \
   -version 1.0.1 -notes "Perbaikan bug" -out latest.json
mylabautoprint-release verify -pub keys/public.key -manifest latest.json -installer MyLabAutoPrint-Setup-1.0.1.exe
```
Lalu buat release `v1.0.1` di repo releases dan lampirkan **dua file**: installer dan `latest.json`.

### 4.3 Domain sendiri

Upload installer + `latest.json` ke folder yang sama, mis. `https://update.domainkamu.com/mylabautoprint/`.
Di klien set `update.source = "url"` dan `update.url = "https://update.domainkamu.com/mylabautoprint/latest.json"`
(bisa dari Pengaturan, API `PUT /config`, `defaults.json`, atau override di rilis GitHub terakhir — lihat bagian 5).

### 4.4 Yang terjadi di klien
1. Cek saat start (setelah 20 detik) lalu tiap `check_interval_hours`.
2. Verifikasi tanda tangan `latest.json` dengan public key di dalam exe. Gagal = diabaikan.
3. `notify`: ikon tray berubah (titik oranye) + menu **"Update tersedia vX – klik untuk pasang"** + banner di Pengaturan.
   `auto`: langsung lanjut (menunggu sampai tidak ada job print).
4. Download installer → cek sha256 → terapkan override config (backup dulu) → jalankan installer `/S`
   → installer menutup aplikasi lama, memasang versi baru, dan menjalankannya lagi.
   Windows menampilkan **UAC "Ya/Tidak"** karena installer memasang ke Program Files.

---

## 5. Override config dari developer

Di setiap rilis kamu bisa memaksa perubahan config ke semua klien:

- GitHub Actions / `release.sh`: isi `release/overrides.txt` (satu per baris, `key=JSON`) dan
  `release/locks.txt` (key yang dikunci), lalu rilis.
- Manual: `-override 'update.repo="kartopolo/repo-baru"' -override 'allowed_origins=["https://app.mylab.id"]' -lock allowed_origins`.

Aturan:
- Tanpa override, semua setting klien (printer, kertas, port, dll.) **dipertahankan** saat update.
- Override menimpa nilai klien **satu kali** saat update dipasang. Sebelumnya `config.json` di-backup ke
  `config.json.bak-YYYYMMDD-HHMMSS`.
- Key yang di-*lock* tidak bisa diubah klien (field di Pengaturan jadi abu-abu, API mengabaikan perubahan dan
  melaporkannya di `ignored_locked`), dan selalu dikembalikan ke nilai dari developer.
- Override ikut ditandatangani, jadi tidak bisa dipalsukan.
- Kosongkan `release/overrides.txt` setelah rilis jika tidak ingin diulang di rilis berikutnya.

---

## 6. Build dari source

Butuh Go ≥ 1.26, NSIS (`makensis`), curl, unzip. Di Linux/WSL:

```bash
sudo apt install nsis
cd source
./build.sh 1.0.0
# hasil: build/mylabautoprint.exe, build/MyLabAutoPrint-Setup-1.0.0.exe,
#        build/mylabautoprint-release.exe, build/mylabautoprint-release-linux, build/mylabautoprint-linux-stub
```

Struktur source:
```
cmd/mylabautoprint/          main, tray (ui_windows.go), resource icon/manifest
cmd/mylabautoprint-release/  alat rilis (keygen/sign/verify)
internal/config/             load/merge/patch/override/lock config
internal/printing/           antrian job, backend Windows (winspool+GDI+SumatraPDF), backend stub (Linux)
internal/server/             HTTP + WebSocket + halaman Pengaturan (web/, di-embed)
internal/update/             manifest ed25519 + updater
internal/platform/           path, autostart, single-instance, dialog
installer/                   NSIS script, defaults.json, icon
client/                      mylabautoprint.js + test.html
tests/                       smoke_http.sh, ws_client_test.js, update_test.sh (jalan terhadap build Linux stub)
third_party/sumatrapdf/      SumatraPDF portable (GPLv3) + fetch.sh
.github/workflows/release.yml
build.sh, release.sh, setup-github.sh
```

Uji di Linux (tanpa printer sungguhan, backend stub menulis file ke `/tmp/mylabautoprint-stub-out`):
```bash
build/mylabautoprint-linux-stub &        # MYLABAUTOPRINT_HOME=/tmp/x untuk folder config terpisah
tests/smoke_http.sh
node --experimental-websocket tests/ws_client_test.js
tests/update_test.sh
```

---

## 7. Troubleshooting

**Web app bilang "MyLab AutoPrint tidak terdeteksi"**
- Cek ikon tray ada. Kalau tidak: Start Menu → MyLab AutoPrint.
- Buka `http://127.0.0.1:47819/status` di browser klien. Harus muncul JSON `"ok":true`.
- Pastikan port di web app sama dengan port di Pengaturan.

**Port sudah dipakai (muncul pesan "port 47819 tidak bisa dipakai")**
- Ada aplikasi lain di port itu. Cek: `netstat -ano | findstr :47819` (kolom terakhir = PID, lihat di Task Manager).
- Ganti port: edit `C:\ProgramData\MyLabAutoPrint\config.json` → `"port": 8282`, lalu jalankan lagi aplikasi.
  Di web app pakai `new MyLabAutoPrint({ ports: [8282, 47819, 8181] })`. Atau kirim override `port=8282` lewat rilis.
- Aplikasi tidak otomatis mencari port lain kalau port terpakai (supaya web app selalu tahu port-nya); pesan error
  muncul dan aplikasi berhenti sampai port diganti.

**Firewall / antivirus**
- Server hanya di `127.0.0.1`, jadi Windows Firewall biasanya tidak bertanya apa-apa.
- Antivirus kadang mengkarantina exe yang belum ditandatangani: tambahkan pengecualian untuk
  `C:\Program Files\MyLabAutoPrint\`. Solusi permanen: tandatangani exe + installer dengan sertifikat code signing.

**SmartScreen "Windows protected your PC"**
- Klik *More info → Run anyway*. Muncul karena tidak ada sertifikat code signing. Hilang setelah exe/installer
  ditandatangani (sertifikat OV/EV) atau setelah reputasi file cukup.

**Printer tidak ditemukan / job error "printer ... tidak bisa dibuka"**
- Nama printer harus **persis** sama dengan di Windows (Settings → Printers & scanners). Lihat daftar di
  `GET /printers` atau di Pengaturan.
- Printer share: nama formatnya `\\NAMA-PC\NamaPrinter`, dan harus sudah ditambahkan di Windows user tersebut.
- Printer thermal USB tanpa driver: pasang driver "Generic / Text Only" atau driver bawaan pabrik, atau pakai printer
  jaringan `tcp://IP:9100`.
- Cek **Lihat Log** untuk pesan error lengkap.

**Print raw ESC/POS tidak keluar / keluar huruf aneh**
- Pakai `type:"raw"` (bukan `text`). Driver printer harus menerima data RAW (driver thermal umumnya bisa).
- Byte > 127 kirim lewat `data` base64.

**PDF tidak tercetak**
- Pastikan `C:\Program Files\MyLabAutoPrint\SumatraPDF.exe` ada (jangan dihapus antivirus).
- Coba *Tes Print → PDF* di Pengaturan. Lihat log untuk pesan dari SumatraPDF.

**CORS / Private Network Access (error di console browser)**
- `origin ... tidak diizinkan` (HTTP 403): tambahkan origin web app (mis. `https://app.domainkamu.com`, persis termasuk
  `https://` dan tanpa `/` di akhir) di *Pengaturan → Website yang diizinkan*, atau `*`.
- `API key salah` (401): isi `apiKey` yang sama dengan Pengaturan.
- Chrome "Private Network Access" / "loopback" diblokir: pastikan web app di **HTTPS** (bukan HTTP publik), dan
  pakai `127.0.0.1` atau `localhost`. Chrome versi baru bisa menampilkan izin *"izinkan situs ini mengakses perangkat
  di jaringan lokal"* – klik **Izinkan**. Header `Access-Control-Allow-Private-Network` sudah dikirim aplikasi.
- Web app di `http://` (bukan https) dari domain publik bisa diblokir Chrome; gunakan HTTPS.

**Update tidak muncul**
- Pengaturan → Update → *Cek update sekarang*, lihat pesan errornya.
- `release ... tidak punya asset latest.json`: lampirkan `latest.json` di release.
- `signature ... TIDAK COCOK`: latest.json ditandatangani key lain / diedit setelah ditandatangani.
- Repo releases harus **public**. Batas API GitHub tanpa login: 60 request/jam per IP (cek tiap 6 jam aman).

**Mengembalikan config lama** – salin `config.json.bak-...` menjadi `config.json`, lalu restart aplikasi.

---

## 8. Keamanan

- Hanya listen di `127.0.0.1`; header `Host` dicek (anti DNS-rebinding).
- Origin browser dicek terhadap `allowed_origins`; opsional API key.
  **Saat ini (v1.0.x) sengaja terbuka: `allowed_origins = ["*"]`, artinya website mana pun bisa mengirim print.**
  Cara membatasi nanti (pilih salah satu):
  1. Per klien: *Pengaturan → Website yang diizinkan*, atau `PUT /config` dengan
     `{"allowed_origins":["https://app.domainkamu.com","https://*.domainkamu.com"]}`, atau edit `config.json`.
  2. Untuk semua instalasi baru: ubah `installer/defaults.json` sebelum build.
  3. Untuk semua klien yang sudah terpasang: di rilis berikutnya isi `release/overrides.txt` dengan
     `allowed_origins=["https://app.domainkamu.com"]` dan (opsional) `release/locks.txt` dengan `allowed_origins`
     supaya klien tidak bisa mengubahnya lagi.
- **API key (opsional, tetap tersedia):** isi `api_key` (Pengaturan / `PUT /config {"api_key":"..."}` / override
  `api_key="..."`). Setelah itu request dari website wajib mengirim header `X-API-Key` (WebSocket: `?apiKey=`),
  di `mylabautoprint.js`: `new MyLabAutoPrint({ apiKey: "..." })`. Kosong = tanpa API key.
- Halaman Pengaturan hanya bisa diakses dari komputer itu sendiri; install update hanya bisa dipicu dari situ atau tray.
- Update: manifest ed25519 + sha256 installer. Public key di dalam exe, bukan di config.

---

## 9. Batasan & catatan

- Windows 10/11 64-bit (Go modern tidak mendukung Windows 7/8).
- HTML: *best-effort* – dirender ke PDF oleh Edge/Chrome headless yang ada di PC klien, lalu dicetak SumatraPDF.
- Word/Excel tidak didukung langsung (konversi ke PDF dulu di server).
- Printer thermal dengan kertas custom: ukuran kertas mengikuti driver (pilihan `paper` hanya ukuran standar).
- Auto-update memunculkan UAC karena terpasang di Program Files.
- `defaults.json` di folder program ditimpa setiap update (itu memang default dari developer); setting klien di
  `config.json` tidak tersentuh.
- SumatraPDF (GPLv3) disertakan apa adanya sebagai program terpisah; lisensinya ada di folder `licenses`.
  Kode sumber: https://github.com/sumatrapdfreader/sumatrapdf

### Catatan RAM
Diukur pada build Linux (backend stub, tanpa tray) — **angka indikatif**, Windows bisa sedikit berbeda:
idle segar ±8 MB RSS, idle setelah puluhan job ±11–15 MB, puncak ±45 MB saat memproses gambar A4 300 dpi,
kembali ke ±11 MB beberapa detik kemudian. PDF dicetak oleh proses SumatraPDF terpisah yang langsung keluar.
