#!/usr/bin/env bash
# Download the official SumatraPDF portable build (if missing) and verify sha256.
set -euo pipefail
cd "$(dirname "$0")"
VER=3.6.1
SHA=719f689b34f47be8ca105ce8484948474dafde0e106bab599e4a89326070c3d0
if [[ -f SumatraPDF.exe ]] && echo "$SHA  SumatraPDF.exe" | sha256sum -c --quiet 2>/dev/null; then exit 0; fi
echo "==> download SumatraPDF $VER portable"
curl -fsSL -o sumatra.zip "https://www.sumatrapdfreader.org/dl/rel/$VER/SumatraPDF-$VER-64.zip"
unzip -o -q sumatra.zip && rm sumatra.zip
mv -f "SumatraPDF-$VER-64.exe" SumatraPDF.exe
echo "$SHA  SumatraPDF.exe" | sha256sum -c
[[ -f COPYING.txt ]] || curl -fsSL -o COPYING.txt https://raw.githubusercontent.com/sumatrapdfreader/sumatrapdf/master/COPYING
