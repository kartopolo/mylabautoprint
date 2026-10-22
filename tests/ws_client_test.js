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
  // profiles over WS + JS API
  const got = [];
  ap.onProfiles(p => got.push(p));
  await ap._call("saveProfile", { name: "wsprof", profile: { printer: "Stub Laser A4", paper: "A5" } });
  await new Promise(r => setTimeout(r, 300));
  ok(got.length > 0 && got[got.length - 1].wsprof && got[got.length - 1].wsprof.paper === "A5", "profiles event pushed over WS");
  const pj = await ap.print({ type: "text", text: "profil via ws", profile: "wsprof" });
  ok(pj.printer === "Stub Laser A4" && pj.settings.paper === "A5", "print with profile via WS");
  const caps = await ap.listPrinters({ capabilities: true });
  ok(caps.printers.some(p => p.capabilities && p.capabilities.duplex), "listPrinters({capabilities}) via WS");
  await ap._call("deleteProfile", { name: "wsprof" });
  // HTTP fallback
  ap.disconnect();
  await new Promise(r => setTimeout(r, 200));
  const hj = await ap.printText("http fallback");
  ok(hj.status === "done", "print via HTTP fallback");
  await ap.saveProfile("jsprof", { printer: "Stub Thermal 80mm", copies: 2 });
  const lp = await ap.listProfiles();
  ok(lp.jsprof && lp.jsprof.copies === 2, "saveProfile/listProfiles via HTTP");
  const hp = await ap.print({ type: "raw", text: "x", profile: "jsprof" });
  ok(hp.printer === "Stub Thermal 80mm" && hp.copies === 2, "print with profile via HTTP");
  const miss = await ap.print({ type: "text", text: "x", profile: "ghost" });
  ok(miss.status === "done" && /ghost/.test((miss.warnings || []).join()), "unknown profile -> done + warning");
  await ap.deleteProfile("jsprof");
  ok(!(await ap.listProfiles()).jsprof, "deleteProfile");
  const c1 = await ap.getCapabilities("Stub Laser A4");
  ok(c1.bins.length === 4, "getCapabilities");
  // auto-reconnect: reconnect then kill the socket server-side is hard; simulate by closing raw socket
  await ap.connect();
  ap._ws.close(); // not via disconnect() -> should auto-reconnect
  await new Promise(r => setTimeout(r, 1500));
  ok(ap.status === "connected", "auto-reconnect after close (statuses: " + statuses.join(">") + ")");
  ap.disconnect();
})().catch(e => { console.error("FAIL", e); process.exitCode = 1; });
