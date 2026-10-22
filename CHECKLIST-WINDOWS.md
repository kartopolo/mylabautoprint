# Checklist Uji di Windows – MyLab AutoPrint

Isi kolom ✅/❌ dan catat pesan error (lihat juga *Lihat Log* di tray). Uji di Windows 10/11 64-bit,
idealnya PC bersih (tanpa QZ Tray), dengan minimal 1 printer biasa + 1 printer thermal (kalau ada).

## A. Instalasi
| # | Langkah | Hasil yang diharapkan | ✅/❌ |
|---|---|---|---|
| A1 | Double-click `MyLabAutoPrint-Setup-1.0.0.exe` | SmartScreen biru muncul → *More info → Run anyway* bisa diklik | |
| A2 | UAC → Ya → Next → Install → Finish (centang "Jalankan") | Selesai tanpa error | |
| A3 | Lihat tray (pojok kanan bawah / panah `^`) | Ikon printer biru muncul | |
| A4 | Settings → Apps | "MyLab AutoPrint" versi 1.0.0 ada, ada tombol Uninstall | |
| A5 | Start Menu | Folder "MyLab AutoPrint" berisi shortcut aplikasi + Uninstall | |
| A6 | Buka `C:\ProgramData\MyLabAutoPrint\` | Ada `config.json` dan `logs\mylabautoprint.log` | |
| A7 | Task Manager → Details → `mylabautoprint.exe` | Memory (private working set) idle ± 10–20 MB — **catat angkanya** | |

## B. Tray
| # | Langkah | Hasil | ✅/❌ |
|---|---|---|---|
| B1 | Klik kiri ikon | Browser membuka `http://127.0.0.1:8181/` (Pengaturan) | |
| B2 | Klik kanan → Status | Kotak pesan: versi, port, jumlah printer, printer default | |
| B3 | Klik kanan → Lihat Log | Log terbuka di Notepad | |
| B4 | Klik kanan → Cek Update | Pesan "Sudah versi terbaru" atau info update / error yang jelas | |
| B5 | Jalankan aplikasi lagi dari Start Menu saat sudah jalan | Tidak muncul ikon kedua; Pengaturan terbuka | |
| B6 | Klik kanan → Keluar | Ikon hilang, `mylabautoprint.exe` tidak ada di Task Manager | |
| B7 | Restart Windows / logout-login | Aplikasi jalan otomatis (ikon muncul) | |

## C. Halaman Pengaturan
| # | Langkah | Hasil | ✅/❌ |
|---|---|---|---|
| C1 | Dropdown printer | Semua printer Windows tampil | |
| C2 | Pilih printer default per tipe → Simpan | "Tersimpan ✓", tetap tersimpan setelah refresh | |
| C3 | Matikan "Jalan otomatis" → Simpan → restart Windows | Aplikasi TIDAK jalan otomatis. Nyalakan lagi sesudahnya | |
| C4 | Ganti port ke 8282 → Simpan | Browser pindah ke `127.0.0.1:8282`; kembalikan ke 8181 | |
| C5 | Riwayat job & Log | Terisi setelah tes print | |

## D. Printing (bagian terpenting)
| # | Langkah | Hasil | ✅/❌ |
|---|---|---|---|
| D1 | Tes Print → Teks (printer biasa) | Tercetak tanpa dialog | |
| D2 | Tes Print → Gambar | Gambar pola tercetak, warna/arah benar, pas di kertas | |
| D3 | Tes Print → PDF | Halaman "TEST PRINT (PDF)" tercetak | |
| D4 | Tes Print → Raw ESC/POS (printer thermal) | Struk tercetak + kertas terpotong (jika printer punya cutter) | |
| D5 | `test.html` → Print PDF dari URL (PDF publik) | Tercetak | |
| D6 | `test.html` → Print file PNG/JPG dan PDF dari komputer | Tercetak | |
| D7 | `test.html` → salinan 2, kertas A5, landscape (printer biasa) | 2 lembar, orientasi/ukuran sesuai | |
| D8 | `test.html` → Print HTML (best-effort) | Tercetak (butuh Edge). Kalau gagal, catat error | |
| D9 | Printer jaringan: isi `tcp://IP-PRINTER:9100` → Print RAW | Tercetak | |
| D10 | Kirim ke printer yang dimatikan / nama salah | Job "error" dengan pesan jelas, aplikasi tidak hang | |
| D11 | Kirim 10 struk cepat berturut-turut | Semua tercetak berurutan | |

## E. Dari web app sungguhan (HTTPS)
| # | Langkah | Hasil | ✅/❌ |
|---|---|---|---|
| E1 | Web app `https://...` (Chrome/Edge) panggil `ap.listPrinters()` | Daftar printer muncul (jika Chrome minta izin "jaringan lokal", klik Izinkan) | |
| E2 | Set *Website yang diizinkan* = domain web app saja | Web app tetap bisa print; situs lain ditolak (403) | |
| E3 | Isi API key di Pengaturan | Web app tanpa key ditolak (401), dengan key berhasil | |
| E4 | WebSocket: status job real-time (queued → printing → done) | Event muncul di web app | |

## F. Update
Siapkan rilis v1.0.1 (GitHub Actions atau `./release.sh 1.0.1 "tes" --publish`).
| # | Langkah | Hasil | ✅/❌ |
|---|---|---|---|
| F1 | Tray → Cek Update | "Versi baru tersedia v1.0.1" → Ya | |
| F2 | UAC → Ya | Aplikasi tertutup, terpasang ulang diam-diam, ikon muncul lagi dalam ±10 detik | |
| F3 | Tray → Status | Versi 1.0.1; setting printer/port lama tetap | |
| F4 | Mode `notify`: tunggu cek otomatis (atau restart aplikasi, cek setelah ±20 detik) | Ikon dapat titik oranye + menu "Update tersedia vX" | |
| F5 | Rilis dengan override (mis. `defaults.paper="A4"` + lock) | Setelah update nilai berubah, field terkunci (abu-abu), ada `config.json.bak-*` | |
| F6 | Ubah 1 karakter di `latest.json` di release (tanpa tanda tangan ulang) → Cek Update | Ditolak: "signature ... TIDAK COCOK" | |

## G. Uninstall
| # | Langkah | Hasil | ✅/❌ |
|---|---|---|---|
| G1 | Settings → Apps → Uninstall | Aplikasi ditutup & dihapus, ditanya hapus pengaturan | |
| G2 | Cek `C:\Program Files\MyLabAutoPrint` dan Start Menu | Hilang | |
| G3 | Restart Windows | Tidak ada yang jalan otomatis | |
| G4 | Install ulang | Berjalan normal (setting lama kembali jika tidak dihapus di G1) | |

Kirim hasil + isi `logs\mylabautoprint.log` jika ada ❌.
