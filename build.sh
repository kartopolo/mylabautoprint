#!/usr/bin/env bash
# Build MyLab AutoPrint on Linux (or WSL / Git Bash with Go + NSIS):
#   ./build.sh 1.0.0
# Output in ./build:
#   mylabautoprint.exe, MyLabAutoPrint-Setup-<ver>.exe,
#   mylabautoprint-release(-linux|.exe), mylabautoprint-linux-stub
# The update public key is read from $PUBKEY_FILE (default release/public.key,
# else ../keys/public.key) or from env MYLABAUTOPRINT_PUBLIC_KEY (base64).
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-${VERSION:-1.0.0}}"
VERSION="${VERSION#v}"
BASEVER="${VERSION%%-*}"
IFS=. read -r V1 V2 V3 _ <<<"$BASEVER.0.0.0"
VERSION4="${V1:-0}.${V2:-0}.${V3:-0}.0"
OUT="$(pwd)/build"
if [[ -z "${PUBKEY_FILE:-}" ]]; then
  if [[ -f release/public.key ]]; then PUBKEY_FILE=release/public.key; else PUBKEY_FILE=../keys/public.key; fi
fi
PUBKEY="${MYLABAUTOPRINT_PUBLIC_KEY:-}"
if [[ -z "$PUBKEY" && -f "$PUBKEY_FILE" ]]; then PUBKEY="$(tr -d '\r\n ' <"$PUBKEY_FILE")"; fi
if [[ -z "$PUBKEY" ]]; then echo "PERINGATAN: public key update tidak ada -> auto-update dinonaktifkan di build ini" >&2; fi
mkdir -p "$OUT"

echo "==> versi $VERSION ($VERSION4)"
echo "==> go vet"
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...

echo "==> resources (icon, manifest, version info)"
go run ./tools/genicon
if ! command -v go-winres >/dev/null; then
  GOWINRES="$(go env GOPATH)/bin/go-winres"
  [[ -x "$GOWINRES" ]] || go install github.com/tc-hib/go-winres@v0.3.3
else GOWINRES=go-winres; fi
"$GOWINRES" make --in winres/winres.json --out cmd/mylabautoprint/rsrc --arch amd64 \
  --product-version "$VERSION4" --file-version "$VERSION4"

LDX="-X main.Version=$VERSION -X mylabautoprint/internal/update.PublicKeyB64=$PUBKEY"
echo "==> windows/amd64 exe"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -H=windowsgui $LDX" -o "$OUT/mylabautoprint.exe" ./cmd/mylabautoprint

echo "==> linux stub (testing)"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $LDX" -o "$OUT/mylabautoprint-linux-stub" ./cmd/mylabautoprint

echo "==> release tool"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUT/mylabautoprint-release-linux" ./cmd/mylabautoprint-release
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUT/mylabautoprint-release.exe" ./cmd/mylabautoprint-release

echo "==> SumatraPDF (bundled for PDF printing)"
./third_party/sumatrapdf/fetch.sh

echo "==> installer (NSIS)"
makensis -V2 -DVERSION="$VERSION" -DVERSION4="$VERSION4" -DBUILDDIR="$OUT" \
  -DOUTFILE="$OUT/MyLabAutoPrint-Setup-$VERSION.exe" installer/mylabautoprint.nsi

ls -la "$OUT"
