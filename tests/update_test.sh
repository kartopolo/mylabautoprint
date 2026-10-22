#!/usr/bin/env bash
# Update signature verification test against the running linux stub (port 8181).
# Needs: build/mylabautoprint-release-linux, ../keys/private.key, python3.
set -u
cd "$(dirname "$0")/.."
B=http://127.0.0.1:8181
REL=build/mylabautoprint-release-linux
W=/tmp/mlt/upd; rm -rf $W; mkdir -p $W/good $W/tampered $W/badsha $W/otherkey
pass=0; fail=0
check() { if grep -q -- "$3" <<<"$2"; then echo "PASS  $1"; pass=$((pass+1)); else echo "FAIL  $1 -> $2"; fail=$((fail+1)); fi; }

head -c 300000 /dev/urandom > $W/good/MyLabAutoPrint-Setup-1.0.1.exe
$REL sign -key ../keys/private.key -installer $W/good/MyLabAutoPrint-Setup-1.0.1.exe -version 1.0.1 \
  -notes "Perbaikan bug" -out $W/good/latest.json -override 'update.check_interval_hours=12' -override 'defaults.paper="A4"' \
  -lock update.check_interval_hours -notes-out $W/good/notes.md >/dev/null
check "release tool verify (good)" "$($REL verify -pub ../keys/public.key -manifest $W/good/latest.json -installer $W/good/MyLabAutoPrint-Setup-1.0.1.exe 2>&1)" "sha256 installer COCOK"

# tampered: change override after signing
cp $W/good/*.exe $W/tampered/; sed 's/"A4"/"A3"/' $W/good/latest.json > $W/tampered/latest.json
check "release tool verify (tampered)" "$($REL verify -pub ../keys/public.key -manifest $W/tampered/latest.json 2>&1)" "TIDAK COCOK"
# bad sha: valid signature, but installer file replaced
cp $W/good/latest.json $W/badsha/; head -c 300000 /dev/urandom > $W/badsha/MyLabAutoPrint-Setup-1.0.1.exe
# other key
$REL keygen -out $W/otherkey/k >/dev/null; cp $W/good/*.exe $W/otherkey/
$REL sign -key $W/otherkey/k/private.key -installer $W/otherkey/MyLabAutoPrint-Setup-1.0.1.exe -version 1.0.1 -out $W/otherkey/latest.json >/dev/null

(cd $W && python3 -m http.server 8765 >/dev/null 2>&1) & SRV=$!
sleep 0.7
setsrc() { curl -s -XPUT $B/config -d "{\"update\":{\"source\":\"url\",\"url\":\"http://127.0.0.1:8765/$1/latest.json\"}}" >/dev/null; }

setsrc tampered
check "app rejects tampered manifest" "$(curl -s -XPOST $B/update/check)" "TIDAK COCOK"
setsrc otherkey
check "app rejects manifest signed by other key" "$(curl -s -XPOST $B/update/check)" "TIDAK COCOK"
setsrc badsha
check "check ok (sig valid)" "$(curl -s -XPOST $B/update/check)" '"available":true'
check "app rejects installer with wrong sha256" "$(curl -s -XPOST $B/update/install)" "sha256 installer tidak cocok"
check "no override applied after sha failure" "$(curl -s $B/config)" '"paper":""'
setsrc good
check "good manifest: update available" "$(curl -s -XPOST $B/update/check)" '"available":true'
INST=$(curl -s -XPOST $B/update/install)
check "good: download+verify ok, reaches installer step (stub cannot run .exe)" "$INST" "hanya bisa dijalankan di Windows"
CFG=$(curl -s $B/config)
check "override applied (paper=A4)" "$CFG" '"paper":"A4"'
check "override applied (interval=12)" "$CFG" '"check_interval_hours":12'
check "locked key listed" "$CFG" '"locked_keys":\["update.check_interval_hours"\]'
check "config backup created" "$(ls /tmp/mlt/home)" "config.json.bak-"
check "client cannot change locked key" "$(curl -s -XPUT $B/config -d '{"update":{"check_interval_hours":1}}')" '"ignored_locked":\["update.check_interval_hours"\]'
check "locked value kept" "$(curl -s $B/config)" '"check_interval_hours":12'
check "installer downloaded" "$(ls /tmp/mylabautoprint/update 2>/dev/null)" "MyLabAutoPrint-Setup-1.0.1.exe"

# GitHub Releases source via fake API
mkdir -p $W/repos/me/rel/releases
cat > $W/repos/me/rel/releases/latest <<J
{"tag_name":"v1.0.1","draft":false,"prerelease":false,"assets":[
 {"name":"latest.json","browser_download_url":"http://127.0.0.1:8765/good/latest.json"},
 {"name":"MyLabAutoPrint-Setup-1.0.1.exe","browser_download_url":"http://127.0.0.1:8765/good/MyLabAutoPrint-Setup-1.0.1.exe"}]}
J
curl -s -XPUT $B/config -d '{"update":{"source":"github","repo":"me/rel"}}' >/dev/null
check "github source (fake API) check" "$(curl -s -XPOST $B/update/check)" '"available":true'

kill $SRV
echo "== $pass passed, $fail failed"
[ $fail -eq 0 ]
