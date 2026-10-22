#!/usr/bin/env bash
# Port default / migration / JS discovery test (linux stub). Uses its own MYLABAUTOPRINT_HOME.
set -u
cd "$(dirname "$0")/.."
BIN="${BIN:-build/mylabautoprint-linux-stub}"
H=/tmp/mlt/porttest; pass=0; fail=0
check() { if grep -q -- "$3" <<<"$2"; then echo "PASS  $1"; pass=$((pass+1)); else echo "FAIL  $1 -> $2"; fail=$((fail+1)); fi; }
start() { MYLABAUTOPRINT_HOME=$H nohup $BIN >/tmp/mlt/porttest.log 2>&1 & PID=$!; sleep 1; }
stop() { kill $PID 2>/dev/null; wait $PID 2>/dev/null; }

# 1. fresh install -> 47819
rm -rf $H; mkdir -p $H; start
check "fresh default port 47819" "$(curl -s 127.0.0.1:47819/status)" '"port":47819'
check "nothing on 8181" "$(curl -s -m 2 127.0.0.1:8181/status || echo closed)" 'closed'
check "config_version written" "$(cat $H/config.json)" '"config_version": 2'
stop
# 2. v1.0.0 config (port 8181, no config_version) -> migrated to 47819
rm -rf $H; mkdir -p $H; echo '{"port":8181,"printers":{"raw":"Stub Thermal 80mm"}}' > $H/config.json; start
check "legacy 8181 config migrated" "$(curl -s 127.0.0.1:47819/status)" '"port":47819'
check "other settings kept" "$(curl -s 127.0.0.1:47819/config)" '"raw":"Stub Thermal 80mm"'
stop
# 3. user deliberately chose 8181 after migration -> kept
python3 - "$H/config.json" <<'PY'
import json,sys; p=sys.argv[1]; c=json.load(open(p)); c["port"]=8181; json.dump(c,open(p,"w"))
PY
start
check "explicit 8181 with config_version 2 kept" "$(curl -s 127.0.0.1:8181/status)" '"port":8181'
check "JS discovers legacy 8181 fallback" "$(EXPECT_PORT=8181 node --experimental-websocket tests/ws_client_test.js 2>/dev/null | grep -c PASS)" '^11$'
# 4. JS discovery follows the app when it moves port (8181 -> 47819 live)
OUT=$(node --experimental-websocket - <<'JS' 2>/dev/null
const M = require("./client/mylabautoprint.js");
const ap = new M({ reconnectDelay: 300 });
(async () => {
  await ap.connect(); const p1 = ap.port;
  await fetch("http://127.0.0.1:" + p1 + "/config", { method: "PUT", body: JSON.stringify({ port: 47819 }) });
  await new Promise(r => setTimeout(r, 2500));
  const j = await ap.printText("after move");
  console.log("moved", p1, "->", ap.port, ap.status, j.status);
  ap.disconnect();
})().catch(e => { console.log("ERR", e.message); });
JS
)
check "JS re-discovers after port change (WS + HTTP)" "$OUT" 'moved 8181 -> 47819 connected done'
stop
echo "== $pass passed, $fail failed"
[ $fail -eq 0 ]
