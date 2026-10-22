#!/usr/bin/env bash
# One-command local release (fallback when GitHub Actions is not used).
#
#   ./release.sh 1.0.1 "Catatan rilis"            # build + sign -> release/out/v1.0.1/
#   ./release.sh 1.0.1 "Catatan rilis" --publish  # + upload to GitHub Releases (gh CLI)
#
# Env:
#   SIGNING_KEY_FILE   private key (default ../keys/private.key)  or
#   MYLABAUTOPRINT_SIGNING_KEY  base64 private key
#   RELEASES_REPO      default kartopolo/mylabautoprint-releases
# Optional files: release/overrides.txt (lines key=JSON), release/locks.txt (lines key)
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:?pakai: ./release.sh VERSI \"catatan\" [--publish]}"; VERSION="${VERSION#v}"
NOTES="${2:-Rilis v$VERSION}"
PUBLISH="${3:-}"
RELEASES_REPO="${RELEASES_REPO:-kartopolo/mylabautoprint-releases}"
KEYARG="${SIGNING_KEY_FILE:-../keys/private.key}"
if [[ -n "${MYLABAUTOPRINT_SIGNING_KEY:-}" ]]; then KEYARG=env; fi
if [[ "$KEYARG" != env && ! -f "$KEYARG" ]]; then echo "private key tidak ditemukan: $KEYARG" >&2; exit 1; fi

./build.sh "$VERSION"
OUT="release/out/v$VERSION"; mkdir -p "$OUT"
SETUP="MyLabAutoPrint-Setup-$VERSION.exe"
cp "build/$SETUP" "$OUT/"

ARGS=()
[[ -f release/overrides.txt ]] && while IFS= read -r l; do [[ -z "$l" || "$l" == \#* ]] || ARGS+=(-override "$l"); done < release/overrides.txt
[[ -f release/locks.txt ]] && while IFS= read -r l; do [[ -z "$l" || "$l" == \#* ]] || ARGS+=(-lock "$l"); done < release/locks.txt

build/mylabautoprint-release-linux sign -key "$KEYARG" -installer "$OUT/$SETUP" -version "$VERSION" \
  -notes "$NOTES" -out "$OUT/latest.json" -notes-out "$OUT/release-notes.md" ${ARGS[@]+"${ARGS[@]}"}
# Make sure the signing key matches the public key embedded in the app.
build/mylabautoprint-release-linux verify -pub release/public.key -manifest "$OUT/latest.json" -installer "$OUT/$SETUP"

echo
echo "Siap rilis: $OUT"; ls -la "$OUT"
if [[ "$PUBLISH" == "--publish" ]]; then
  gh release create "v$VERSION" "$OUT/$SETUP" "$OUT/latest.json" --repo "$RELEASES_REPO" \
    --title "MyLab AutoPrint v$VERSION" --notes-file "$OUT/release-notes.md"
  echo "Dipublikasikan: https://github.com/$RELEASES_REPO/releases/tag/v$VERSION"
else
  cat <<MSG
Langkah berikut (pilih salah satu):
  GitHub : ./release.sh $VERSION "$NOTES" --publish
           (atau upload $SETUP + latest.json manual ke release baru tag v$VERSION di $RELEASES_REPO)
  Domain : upload $SETUP dan latest.json ke folder yang sama di server, mis.
           https://update.domainkamu.com/mylabautoprint/ lalu set update.source="url",
           update.url="https://update.domainkamu.com/mylabautoprint/latest.json"
MSG
fi
