/*!
 * mylabautoprint.js - client library for MyLab AutoPrint (local print server)
 * Works in browsers (and Node 20+ with --experimental-websocket / Node 22+).
 *
 *   const ap = new MyLabAutoPrint({ port: 8181, apiKey: "" });
 *   ap.onStatus(s => console.log("koneksi:", s));      // connecting|connected|disconnected
 *   ap.onJob(job => console.log(job.id, job.status));  // queued|printing|done|error
 *   await ap.connect();                                // WebSocket (optional, auto-reconnect)
 *   const { printers, default: def } = await ap.listPrinters();
 *   await ap.print({ type: "raw", printer: "POS-80", text: "\x1b@Halo\n\n\n\x1dVB\x00" });
 *   await ap.printPDF("https://contoh.com/invoice.pdf", { printer: "HP LaserJet", paper: "A4" });
 */
(function (root, factory) {
  if (typeof module === "object" && module.exports) module.exports = factory();
  else root.MyLabAutoPrint = factory();
})(typeof self !== "undefined" ? self : this, function () {
  "use strict";

  function MyLabAutoPrint(opts) {
    opts = opts || {};
    this.host = opts.host || "127.0.0.1";
    this.port = opts.port || 8181;
    this.apiKey = opts.apiKey || "";
    this.autoReconnect = opts.autoReconnect !== false;
    this.reconnectDelay = opts.reconnectDelay || 3000;
    this.timeout = opts.timeout || 180000;
    this.status = "disconnected";
    this._ws = null;
    this._seq = 0;
    this._pending = {};
    this._waiters = {};
    this._finished = {}; // job events can arrive before the print response
    this._finishedIds = [];
    this._listeners = { status: [], job: [], printers: [], update: [], message: [] };
    this._wantWS = false;
  }

  var P = MyLabAutoPrint.prototype;

  P.baseURL = function () { return "http://" + this.host + ":" + this.port; };

  // ---- events --------------------------------------------------------------
  P.on = function (ev, fn) { (this._listeners[ev] = this._listeners[ev] || []).push(fn); return this; };
  P.off = function (ev, fn) { var l = this._listeners[ev] || []; var i = l.indexOf(fn); if (i >= 0) l.splice(i, 1); return this; };
  P.onStatus = function (fn) { return this.on("status", fn); };
  P.onJob = function (fn) { return this.on("job", fn); };
  P.onPrinters = function (fn) { return this.on("printers", fn); };
  P._emit = function (ev, data) { (this._listeners[ev] || []).slice().forEach(function (fn) { try { fn(data); } catch (e) { console.error(e); } }); };
  P._setStatus = function (s) { if (this.status !== s) { this.status = s; this._emit("status", s); } };

  // ---- HTTP ----------------------------------------------------------------
  P.request = function (method, path, body) {
    var headers = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (this.apiKey) headers["X-API-Key"] = this.apiKey;
    return fetch(this.baseURL() + path, {
      method: method, headers: headers, body: body !== undefined ? JSON.stringify(body) : undefined
    }).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok || j.ok === false) {
          var e = new Error(j.error || ("HTTP " + r.status)); e.response = j; e.status = r.status; throw e;
        }
        return j;
      });
    });
  };

  /** true if the local app is running and reachable. */
  P.isAvailable = function () { return this.request("GET", "/status").then(function () { return true; }, function () { return false; }); };
  P.getStatus = function () { return this.request("GET", "/status"); };
  P.listPrinters = function () {
    if (this._wsOpen()) return this._call("printers").then(function (r) { return r; });
    return this.request("GET", "/printers");
  };
  P.jobs = function () { return this.request("GET", "/jobs").then(function (r) { return r.jobs; }); };
  P.getConfig = function () { return this.request("GET", "/config").then(function (r) { return r.config; }); };
  /** Partial update, e.g. setConfig({printers:{raw:"POS-80"}}) */
  P.setConfig = function (patch) { return this.request("PUT", "/config", patch); };

  // ---- WebSocket -------------------------------------------------------------
  P._wsOpen = function () { return this._ws && this._ws.readyState === 1; };

  /** Open the WebSocket (optional). Resolves when connected. Auto-reconnects. */
  P.connect = function () {
    var self = this;
    this._wantWS = true;
    if (this._wsOpen()) return Promise.resolve();
    return new Promise(function (resolve, reject) {
      var url = "ws://" + self.host + ":" + self.port + "/ws" + (self.apiKey ? "?apiKey=" + encodeURIComponent(self.apiKey) : "");
      var ws;
      try { ws = new WebSocket(url); } catch (e) { reject(e); return; }
      self._ws = ws;
      self._setStatus("connecting");
      var opened = false;
      ws.onopen = function () { opened = true; self._setStatus("connected"); resolve(); };
      ws.onmessage = function (e) {
        var m; try { m = JSON.parse(e.data); } catch (_) { return; }
        self._emit("message", m);
        if (m.type === "response") {
          var p = self._pending[m.id];
          if (p) { delete self._pending[m.id]; clearTimeout(p.t); m.ok ? p.resolve(m.result) : p.reject(new Error(m.error)); }
        } else if (m.type === "job") {
          self._emit("job", m.job);
          if (m.job.status === "done" || m.job.status === "error") {
            self._finished[m.job.id] = m.job;
            self._finishedIds.push(m.job.id);
            if (self._finishedIds.length > 200) delete self._finished[self._finishedIds.shift()];
          }
          var w = self._waiters[m.job.id];
          if (w && (m.job.status === "done" || m.job.status === "error")) {
            delete self._waiters[m.job.id];
            m.job.status === "done" ? w.resolve(m.job) : w.reject(Object.assign(new Error(m.job.error), { job: m.job }));
          }
        } else if (m.type === "printers") self._emit("printers", m);
        else if (m.type === "update") self._emit("update", m.update);
      };
      ws.onclose = function () {
        self._setStatus("disconnected");
        Object.keys(self._pending).forEach(function (id) { self._pending[id].reject(new Error("koneksi terputus")); delete self._pending[id]; });
        if (!opened) reject(new Error("MyLab AutoPrint tidak terhubung di " + url));
        if (self._wantWS && self.autoReconnect) {
          setTimeout(function () { if (self._wantWS) self.connect().catch(function () {}); }, self.reconnectDelay);
        }
      };
      ws.onerror = function () { /* onclose follows */ };
    });
  };

  P.disconnect = function () { this._wantWS = false; if (this._ws) this._ws.close(); };

  P._call = function (action, params) {
    var self = this, id = String(++this._seq);
    return new Promise(function (resolve, reject) {
      var t = setTimeout(function () { delete self._pending[id]; reject(new Error("timeout")); }, self.timeout);
      self._pending[id] = { resolve: resolve, reject: reject, t: t };
      self._ws.send(JSON.stringify({ id: id, action: action, params: params }));
    });
  };

  // ---- printing ----------------------------------------------------------------
  /**
   * Print. req = {type: raw|pdf|image|text|html, printer?, data?(base64), text?, url?,
   *               copies?, paper?, orientation?, jobName?, scale?, pages?, textMode?, fontSize?}
   * opts.wait (default true): resolve when the job is done (reject on error).
   * Resolves with the job object.
   */
  P.print = function (req, opts) {
    var self = this, wait = !opts || opts.wait !== false;
    if (this._wsOpen()) {
      return this._call("print", req).then(function (r) {
        var job = self._finished[r.job.id] || r.job;
        if (!wait || job.status === "done") return job;
        if (job.status === "error") throw Object.assign(new Error(job.error), { job: job });
        return new Promise(function (resolve, reject) {
          self._waiters[job.id] = { resolve: resolve, reject: reject };
        });
      });
    }
    var body = Object.assign({}, req, { async: !wait });
    return this.request("POST", "/print", body).then(function (r) { return r.job; });
  };

  function withType(type, src, opts) {
    var r = Object.assign({ type: type }, opts || {});
    if (typeof src === "string" && /^https?:\/\//i.test(src)) r.url = src;
    else if (typeof src === "string" && /^data:/.test(src)) r.data = src.slice(src.indexOf(",") + 1);
    else if (typeof src === "string" && (type === "raw" || type === "text" || type === "html")) r.text = src;
    else if (typeof src === "string") r.data = src; // assume base64
    else return MyLabAutoPrint.toBase64(src).then(function (b) { r.data = b; return r; });
    return Promise.resolve(r);
  }
  P.printRaw = function (src, opts) { var s = this; return withType("raw", src, opts).then(function (r) { return s.print(r); }); };
  P.printText = function (src, opts) { var s = this; return withType("text", src, opts).then(function (r) { return s.print(r); }); };
  P.printPDF = function (src, opts) { var s = this; return withType("pdf", src, opts).then(function (r) { return s.print(r); }); };
  P.printImage = function (src, opts) { var s = this; return withType("image", src, opts).then(function (r) { return s.print(r); }); };
  P.printHTML = function (src, opts) { var s = this; return withType("html", src, opts).then(function (r) { return s.print(r); }); };
  P.testPrint = function (type, printer) { return this.request("POST", "/test-print", { type: type || "text", printer: printer || "" }); };

  /** Convert Uint8Array | ArrayBuffer | Blob | number[] to base64 (Promise). */
  MyLabAutoPrint.toBase64 = function (src) {
    if (typeof Blob !== "undefined" && src instanceof Blob) {
      return src.arrayBuffer().then(MyLabAutoPrint.toBase64);
    }
    var bytes = src instanceof ArrayBuffer ? new Uint8Array(src) : (src instanceof Uint8Array ? src : new Uint8Array(src));
    var bin = "";
    for (var i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    return Promise.resolve(typeof btoa === "function" ? btoa(bin) : Buffer.from(bytes).toString("base64"));
  };

  /** ESC/POS helper: text with init + optional cut. */
  MyLabAutoPrint.escpos = function (text, cut) {
    return "\x1b@" + text + "\n\n\n" + (cut === false ? "" : "\x1dVB\x00");
  };

  return MyLabAutoPrint;
});
