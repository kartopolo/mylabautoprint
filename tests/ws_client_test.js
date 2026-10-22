// Node 20: node --experimental-websocket ws_client_test.js
const MyLabAutoPrint = require("../client/mylabautoprint.js");
const ap = new MyLabAutoPrint(process.env.PORT ? { port: +process.env.PORT, reconnectDelay: 500 } : { reconnectDelay: 500 }); // default: auto-discover
const events = [], statuses = [];
let printersEv = null;
ap.onJob(j => events.push(j.status));
ap.onStatus(s => statuses.push(s));
ap.onPrinters(p => (printersEv = p));
const ok = (c, m) => { console.log((c ? "PASS  " : "FAIL  ") + m); if (!c) process.exitCode = 1; };
(async () => {
  ok(await ap.isAvailable(), "isAvailable");
  ok(ap.port === +(process.env.EXPECT_PORT || 47819), "discovered port " + ap.port);
  await ap.connect();
  ok(ap.status === "connected", "ws connected");
  await new Promise(r => setTimeout(r, 300));
  ok(printersEv && printersEv.printers.length > 0, "printers pushed on connect");
  const p = await ap.listPrinters();
  ok(p.default === "Stub Thermal 80mm", "listPrinters via ws");
  const job = await ap.printRaw(MyLabAutoPrint.escpos("WS test"), { copies: 1 });
  ok(job.status === "done", "print via ws resolves done");
  ok(events.join(",").includes("queued") && events.includes("printing") && events.includes("done"), "job events queued/printing/done: " + events.join(","));
  try { await ap.print({ type: "raw", text: "x", printer: "Nope" }); ok(false, "error job rejects"); }
  catch (e) { ok(/tidak ditemukan/.test(e.message), "error job rejects: " + e.message); }
  const img = await ap.printImage(new Uint8Array(require("fs").readFileSync(__dirname + "/../internal/server/web/icon.png")));
  ok(img.status === "done", "printImage(Uint8Array) via ws");
  // HTTP fallback
  ap.disconnect();
  await new Promise(r => setTimeout(r, 200));
  const hj = await ap.printText("http fallback");
  ok(hj.status === "done", "print via HTTP fallback");
  // auto-reconnect: reconnect then kill the socket server-side is hard; simulate by closing raw socket
  await ap.connect();
  ap._ws.close(); // not via disconnect() -> should auto-reconnect
  await new Promise(r => setTimeout(r, 1500));
  ok(ap.status === "connected", "auto-reconnect after close (statuses: " + statuses.join(">") + ")");
  ap.disconnect();
})().catch(e => { console.error("FAIL", e); process.exitCode = 1; });
