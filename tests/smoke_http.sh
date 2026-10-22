#!/usr/bin/env bash
# HTTP smoke test against a running build (linux stub or Windows), default port 47819.
B="${BASE:-http://127.0.0.1:47819}"
pass=0; fail=0
check() { # name, condition-output, expected-substring
  if grep -q -- "$3" <<<"$2"; then echo "PASS  $1"; pass=$((pass+1)); else echo "FAIL  $1 -> $2"; fail=$((fail+1)); fi
}
b64() { base64 -w0; }
check "GET /status"   "$(curl -s $B/status)" '"ok":true'
check "GET /printers" "$(curl -s $B/printers)" '"default":'
check "GET / (UI)"    "$(curl -s $B/)" 'MyLab AutoPrint - Pengaturan'
check "raw print"     "$(curl -s -XPOST $B/print -d "{\"type\":\"raw\",\"data\":\"$(printf '\x1b@HALO\n\x1dVB\x00' | b64)\"}")" '"status":"done"'
check "text print"    "$(curl -s -XPOST $B/print -d '{"type":"text","text":"halo dunia","copies":2,"printer":"Stub Laser A4"}')" '"copies":2'
PNG=$(python3 -c "import base64,zlib,struct
def c(t,d): return struct.pack('>I',len(d))+t+d+struct.pack('>I',zlib.crc32(t+d))
raw=b''.join(b'\x00'+b'\xff\x00\x00'*4 for _ in range(3))
print(base64.b64encode(b'\x89PNG\r\n\x1a\n'+c(b'IHDR',struct.pack('>IIBBBBB',4,3,8,2,0,0,0))+c(b'IDAT',zlib.compress(raw))+c(b'IEND',b'')).decode())")
check "image print"   "$(curl -s -XPOST $B/print -d "{\"type\":\"image\",\"data\":\"$PNG\"}")" '"status":"done"'
check "image invalid" "$(curl -s -XPOST $B/print -d '{"type":"image","data":"aGVsbG8="}')" 'gambar tidak bisa dibaca'
check "pdf via test-print" "$(curl -s -XPOST $B/test-print -d '{"type":"pdf"}')" '"ok":true'
check "pdf not pdf"   "$(curl -s -XPOST $B/print -d '{"type":"pdf","data":"aGVsbG8="}')" 'bukan PDF'
check "unknown type"  "$(curl -s -XPOST $B/print -d '{"type":"docx","text":"x"}')" 'type tidak dikenal'
check "no data"       "$(curl -s -XPOST $B/print -d '{"type":"raw"}')" 'tidak ada data'
check "bad printer"   "$(curl -s -XPOST $B/print -d '{"type":"raw","text":"x","printer":"Nope"}')" 'tidak ditemukan'
ASYNC=$(curl -s -XPOST $B/print -d '{"type":"text","text":"async","async":true}')
check "async queued"  "$ASYNC" '"status":"queued"'
ID=$(sed -E 's/.*"id":"([^"]+)".*/\1/' <<<"$ASYNC"); sleep 0.6
check "GET /jobs/{id}" "$(curl -s $B/jobs/$ID)" '"status":"done"'
check "GET /jobs"     "$(curl -s $B/jobs)" '"jobs":\['
# CORS + Private Network Access preflight
PRE=$(curl -s -i -XOPTIONS $B/print -H 'Origin: https://app.example.com' -H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Private-Network: true')
check "preflight 204"  "$PRE" '204'
check "preflight PNA"  "$PRE" 'Access-Control-Allow-Private-Network: true'
check "preflight ACAO" "$PRE" 'Access-Control-Allow-Origin: https://app.example.com'
check "DNS rebinding host blocked" "$(curl -s -H 'Host: evil.com:47819' $B/status)" 'host tidak diizinkan'
# restrict origins + api key
curl -s -XPUT $B/config -d '{"allowed_origins":["https://*.mylab.id","https://app.example.com"],"api_key":"rahasia123"}' >/dev/null
check "origin blocked"  "$(curl -s -XPOST $B/print -H 'Origin: https://evil.com' -d '{"type":"text","text":"x"}')" 'tidak diizinkan'
check "api key needed"  "$(curl -s -XPOST $B/print -H 'Origin: https://app.example.com' -d '{"type":"text","text":"x"}')" 'API key salah'
check "api key ok"      "$(curl -s -XPOST $B/print -H 'Origin: https://app.example.com' -H 'X-API-Key: rahasia123' -d '{"type":"text","text":"x"}')" '"status":"done"'
check "wildcard origin" "$(curl -s -XPOST $B/print -H 'Origin: https://kasir.mylab.id' -H 'X-API-Key: rahasia123' -d '{"type":"text","text":"x"}')" '"status":"done"'
check "self origin no key" "$(curl -s -XPOST $B/print -H 'Origin: http://127.0.0.1:47819' -d '{"type":"text","text":"x"}')" '"status":"done"'
curl -s -XPUT $B/config -d '{"allowed_origins":["*"],"api_key":""}' >/dev/null
# per-type default printer
curl -s -XPUT $B/config -d '{"printers":{"text":"Stub Laser A4"}}' >/dev/null
check "per-type default printer" "$(curl -s -XPOST $B/print -d '{"type":"text","text":"x"}')" '"printer":"Stub Laser A4"'
curl -s -XPUT $B/config -d '{"printers":{"text":""}}' >/dev/null
check "config sanitize" "$(curl -s -XPUT $B/config -d '{"defaults":{"copies":500,"orientation":"weird"}}')" '"orientation":"portrait","copies":99'
curl -s -XPUT $B/config -d '{"defaults":{"copies":1}}' >/dev/null
check "GET /log"      "$(curl -s "$B/log?lines=5")" '"lines":'
check "GET /update"   "$(curl -s $B/update)" '"key_embedded":true'
echo "== $pass passed, $fail failed"
[ $fail -eq 0 ]
