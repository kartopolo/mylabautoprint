/*!
 * mylabautoprint.js - client library for MyLab AutoPrint (local print server)
 * Works in browsers (and Node 20+ with --experimental-websocket / Node 22+).
 *
 *   const ap = new MyLabAutoPrint({ apiKey: "" });     // port auto-detected (47819, then legacy 8181)
 *   // or fixed: new MyLabAutoPrint({ port: 47819 })    // or a custom list: { ports: [47819, 50001] }
 *   ap.onStatus(s => console.log("koneksi:", s));      // connecting|connected|disconnected
 *   ap.onJob(job => console.log(job.id, job.status));  // queued|printing|done|error
 *   await ap.connect();                                // WebSocket (optional, auto-reconnect)
 *   const { printers, default: def } = await ap.listPrinters();
 *   await ap.print({ type: "raw", printer: "POS-80", text: "\x1b@Halo\n\n\n\x1dVB\x00" });
 *   await ap.printPDF("https://contoh.com/invoice.pdf", { printer: "HP LaserJet", paper: "A4" });
 *   await ap.saveProfile("invoice", { printer: "HP LaserJet", paper: "A4", duplex: "long" });
 *   await ap.print({ profile: "invoice", type: "pdf", url: "https://contoh.com/inv.pdf" });
 */
(function (root, factory) {
  if (typeof module === "object" && module.exports) module.exports = factory();
  else root.MyLabAutoPrint = factory();
})(typeof self !== "undefined" ? self : this, function () {
  "use strict";

  function MyLabAutoPrint(opts) {
    opts = opts || {};
    this.host = opts.host || "127.0.0.1";
    // Port discovery: an explicit `port` is used as-is; otherwise the ports in
    // `ports` are probed in order (GET /status) and the first answering one wins.
    this.ports = opts.port ? [opts.port] : (opts.ports || MyLabAutoPrint.DEFAULT_PORTS).slice();
    this.port = this.ports[0];
    this._discovered = !!opts.port;
    this._discovering = null;
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
    this._listeners = { status: [], job: [], printers: [], update: [], message: [], profiles: [] };
    this._wantWS = false;
  }

  /** Ports probed when no explicit port is given: current default first, then the v1.0.0 legacy port. */
  MyLabAutoPrint.DEFAULT_PORTS = [47819, 8181];

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
  /** Find the port the local app listens on (tries `ports` in order). Resolves with the port. */
  P.discover = function (force) {
    var self = this;
    if (this._discovered && !force) return Promise.resolve(this.port);
    if (this._discovering) return this._discovering;
    var i = 0;
    function next() {
      if (i >= self.ports.length) {
        self._discovering = null;
        return Promise.reject(new Error("MyLab AutoPrint tidak ditemukan di port " + self.ports.join(", ")));
      }
      var port = self.ports[i++];
      return probe(self.host, port).then(function (ok) {
        if (ok) { self.port = port; self._discovered = true; self._discovering = null; return port; }
        return next();
      });
    }
    this._discovering = next();
    return this._discovering;
  };

  function probe(host, port) {
    var ctrl = typeof AbortController !== "undefined" ? new AbortController() : null;
    var t = setTimeout(function () { if (ctrl) ctrl.abort(); }, 1500);
    return fetch("http://" + host + ":" + port + "/status", ctrl ? { signal: ctrl.signal } : {})
      .then(function (r) { return r.json(); })
      .then(function (j) { return !!(j && j.app === "MyLab AutoPrint"); }, function () { return false; })
      .then(function (ok) { clearTimeout(t); return ok; });
  }

  P.request = function (method, path, body) {
    var self = this;
    return this.discover().then(function () {
      return self._request(method, path, body).catch(function (e) {
        // network error (no HTTP status): the app may have moved to another port -> rediscover once
        if (e.status === undefined && self.ports.length > 1) {
          self._discovered = false;
          return self.discover(true).then(function () { return self._request(method, path, body); });
        }
        throw e;
      });
    }, function (e) {
      // nothing found: still try the first port so the caller gets a normal network error
      if (self.ports.length > 1) throw e;
      return self._request(method, path, body);
    });
  };

  P._request = function (method, path, body) {
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
  /**
   * List printers. listPrinters({capabilities:true}) adds per-printer capabilities
   * (papers, bins/trays, duplex, color) - slower, the driver is queried.
   */
  P.listPrinters = function (opts) {
    var caps = opts && opts.capabilities;
    if (this._wsOpen()) return this._call(caps ? "printersWithCapabilities" : "printers");
    return this.request("GET", "/printers" + (caps ? "?capabilities=1" : ""));
  };
  /** Capabilities of one printer: {papers:[{id,name,width_mm,height_mm}], bins:[{id,name}], duplex, color, max_copies} */
  P.getCapabilities = function (printer) {
    return this.request("GET", "/printers/capabilities?name=" + encodeURIComponent(printer)).then(function (r) { return r.capabilities; });
  };

  // ---- print profiles ------------------------------------------------------
  // A profile maps a name used by the web app ("label", "invoice") to a printer + settings on this PC:
  // {printer, paper, orientation, copies, scale, color: "color"|"mono", duplex: "simplex"|"long"|"short",
  //  tray, margin_mm, cut, font_size, text_mode}
  P.listProfiles = function () { return this.request("GET", "/profiles").then(function (r) { return r.profiles; }); };
  P.getProfile = function (name) { return this.request("GET", "/profiles/" + encodeURIComponent(name)).then(function (r) { return r.profile; }); };
  P.saveProfile = function (name, profile) {
    return this.request("PUT", "/profiles/" + encodeURIComponent(name), profile || {}).then(function (r) { return r.profile; });
  };
  P.deleteProfile = function (name) { return this.request("DELETE", "/profiles/" + encodeURIComponent(name)); };
  P.onProfiles = function (fn) { return this.on("profiles", fn); };
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
    return this.discover().catch(function () { /* fall through: try current port */ }).then(function () { return self._connectWS(); });
  };

  P._connectWS = function () {
    var self = this;
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
        else if (m.type === "profiles") self._emit("profiles", m.profiles);
      };
      ws.onclose = function () {
        self._setStatus("disconnected");
        Object.keys(self._pending).forEach(function (id) { self._pending[id].reject(new Error("koneksi terputus")); delete self._pending[id]; });
        if (!opened) reject(new Error("MyLab AutoPrint tidak terhubung di " + url));
        if (self._wantWS && self.autoReconnect) {
          setTimeout(function () {
            if (!self._wantWS) return;
            if (self.ports.length > 1) self._discovered = false; // app may have moved port (e.g. after update)
            self.connect().catch(function () {});
          }, self.reconnectDelay);
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
   * Print. req = {type: raw|pdf|image|text|html, profile?, printer?, data?(base64), text?, url?,
   *               copies?, paper?, orientation?, jobName?, scale?, pages?, textMode?, fontSize?,
   *               color?, duplex?, tray?, marginMm?, cut?}
   * Explicit fields override the profile; the profile overrides per-type defaults.
   * Unknown profile -> job still prints with defaults and job.warnings explains why.
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
