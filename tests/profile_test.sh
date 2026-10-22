#!/usr/bin/env bash
# Print profiles: CRUD, validation, capabilities, resolution order (linux stub on 47819).
set -u
B="${BASE:-http://127.0.0.1:47819}"
pass=0; fail=0
check() { if grep -q -- "$3" <<<"$2"; then echo "PASS  $1"; pass=$((pass+1)); else echo "FAIL  $1 -> $2"; fail=$((fail+1)); fi; }
nocheck() { if grep -q -- "$3" <<<"$2"; then echo "FAIL  $1 -> $2"; fail=$((fail+1)); else echo "PASS  $1"; pass=$((pass+1)); fi; }
J='Content-Type: application/json'
curl -s -XPUT $B/config -d '{"printers":{"default":"","text":"","raw":"","pdf":"","image":""},"allowed_origins":["*"],"api_key":""}' >/dev/null
curl -s -XDELETE $B/profiles/invoice >/dev/null; curl -s -XDELETE $B/profiles/label >/dev/null

# capabilities
check "GET /printers?capabilities=1" "$(curl -s "$B/printers?capabilities=1")" '"bins":\[{"id":7,"name":"Automatic"}'
check "GET /printers/capabilities?name=" "$(curl -s "$B/printers/capabilities?name=Stub%20Laser%20A4")" '"duplex":true'
check "capabilities unknown printer 404" "$(curl -s "$B/printers/capabilities?name=Nope")" 'tidak ditemukan'

# CRUD
check "create invoice" "$(curl -s -XPUT $B/profiles/invoice -H "$J" -d '{"printer":"Stub Laser A4","paper":"A4","duplex":"long","color":"mono","copies":2,"tray":"Tray 2"}')" '"ok":true'
check "create label (POST)" "$(curl -s -XPOST $B/profiles/label -H "$J" -d '{"printer":"Stub Thermal 80mm","paper":"100x150mm","margin_mm":2,"cut":true,"scale":"width"}')" '"ok":true'
check "GET /profiles" "$(curl -s $B/profiles)" '"invoice":{"printer":"Stub Laser A4"'
check "GET /profiles/label" "$(curl -s $B/profiles/label)" '"paper":"100x150mm"'
check "update invoice" "$(curl -s -XPUT $B/profiles/invoice -H "$J" -d '{"printer":"Stub Laser A4","paper":"A5","duplex":"long","color":"mono","copies":2,"tray":"Tray 2"}')" '"paper":"A5"'
check "profiles persisted in config" "$(curl -s $B/config)" '"profiles":{"invoice"'
check "invalid name" "$(curl -s -XPUT "$B/profiles/bad%2Fname%3F" -H "$J" -d '{}')" 'nama profil tidak valid'
check "invalid duplex" "$(curl -s -XPUT $B/profiles/x -H "$J" -d '{"duplex":"sideways"}')" 'duplex harus'
check "unknown field rejected" "$(curl -s -XPUT $B/profiles/x -H "$J" -d '{"printr":"typo"}')" 'unknown field'
check "GET missing 404" "$(curl -s $B/profiles/nope)" 'profil tidak ditemukan'
check "DELETE missing 404" "$(curl -s -XDELETE $B/profiles/nope)" 'profil tidak ditemukan'
check "CORS preflight allows DELETE" "$(curl -s -i -XOPTIONS $B/profiles/label -H 'Origin: https://app.example.com' -H 'Access-Control-Request-Method: DELETE')" 'Allow-Methods: GET, POST, PUT, DELETE'

# origin / api key rules same as config writes
curl -s -XPUT $B/config -d '{"allowed_origins":["https://app.example.com"],"api_key":"k123"}' >/dev/null
check "profile write: foreign origin blocked" "$(curl -s -XPUT $B/profiles/z -H 'Origin: https://evil.com' -d '{}')" 'tidak diizinkan'
check "profile write: api key required" "$(curl -s -XPUT $B/profiles/z -H 'Origin: https://app.example.com' -d '{}')" 'API key salah'
check "profile write: with api key ok" "$(curl -s -XPUT $B/profiles/z -H 'Origin: https://app.example.com' -H 'X-API-Key: k123' -d '{"printer":"Stub Laser A4"}')" '"ok":true'
check "profile delete: with api key ok" "$(curl -s -XDELETE $B/profiles/z -H 'Origin: https://app.example.com' -H 'X-API-Key: k123')" '"deleted":"z"'
curl -s -XPUT $B/config -d '{"allowed_origins":["*"],"api_key":""}' >/dev/null

# resolution order
R=$(curl -s -XPOST $B/print -d '{"type":"text","text":"x","profile":"invoice"}')
check "profile printer used" "$R" '"printer":"Stub Laser A4"'
check "profile settings applied" "$R" '"paper":"A5","orientation":"portrait","color":"mono","duplex":"long","tray":"Tray 2"'
check "profile copies applied" "$R" '"copies":2'
check "job records profile" "$R" '"profile":"invoice"'
R=$(curl -s -XPOST $B/print -d '{"type":"text","text":"x","profile":"invoice","printer":"Stub Thermal 80mm","paper":"Letter","copies":1,"duplex":"short"}')
check "explicit fields override profile" "$R" '"printer":"Stub Thermal 80mm"'
check "explicit paper/duplex override" "$R" '"paper":"Letter","orientation":"portrait","color":"mono","duplex":"short"'
check "explicit copies override" "$R" '"copies":1,'
curl -s -XPUT $B/config -d '{"printers":{"text":"Stub Thermal 80mm","default":"Stub Laser A4"}}' >/dev/null
check "profile printer beats per-type default" "$(curl -s -XPOST $B/print -d '{"type":"text","text":"x","profile":"invoice"}')" '"printer":"Stub Laser A4"'
curl -s -XPUT $B/profiles/noprinter -H "$J" -d '{"paper":"A4"}' >/dev/null
check "profile w/o printer -> per-type default" "$(curl -s -XPOST $B/print -d '{"type":"text","text":"x","profile":"noprinter"}')" '"printer":"Stub Thermal 80mm"'
check "profile w/o printer, type w/o default -> 'default'" "$(curl -s -XPOST $B/print -d '{"type":"raw","text":"x","profile":"noprinter"}')" '"printer":"Stub Laser A4"'
curl -s -XPUT $B/config -d '{"printers":{"text":"","default":""}}' >/dev/null
check "no defaults -> Windows default" "$(curl -s -XPOST $B/print -d '{"type":"text","text":"x","profile":"noprinter"}')" '"printer":"Stub Thermal 80mm"'
R=$(curl -s -XPOST $B/print -d '{"type":"text","text":"x","profile":"ghost"}')
check "missing profile: still prints" "$R" '"status":"done"'
check "missing profile: warning in job" "$R" 'profil \\"ghost\\" tidak ditemukan'
check "missing profile: warnings top-level" "$R" '"warnings":\["profil'
# raw cut from profile
OUTD=${MYLABAUTOPRINT_STUB_OUT:-/tmp/mlt/out}
R=$(curl -s -XPOST $B/print -d '{"type":"raw","text":"LABEL","profile":"label"}')
check "label profile raw: cut flag" "$R" '"cut":true'
F=$(ls -t $OUTD/*raw_Stub_Thermal* | head -1)
check "cut bytes appended (GS V B 0)" "$(od -An -tx1 "$F" | tr -d ' \n')" '4c4142454c0a0a0a1d564200'
R=$(curl -s -XPOST $B/print -d '{"type":"raw","text":"NOCUT","profile":"label","cut":false}')
nocheck "explicit cut:false overrides profile" "$R" '"cut":true'
check "cut on non-raw -> warning" "$(curl -s -XPOST $B/print -d '{"type":"text","text":"x","cut":true}')" 'cut hanya berlaku'
check "test-print with profile" "$(curl -s -XPOST $B/test-print -d '{"type":"image","profile":"label"}')" '"profile":"label"'
check "DELETE label" "$(curl -s -XDELETE $B/profiles/label)" '"deleted":"label"'
curl -s -XDELETE $B/profiles/noprinter >/dev/null
nocheck "label gone" "$(curl -s $B/profiles)" '"label"'
echo "== $pass passed, $fail failed"
[ $fail -eq 0 ]
