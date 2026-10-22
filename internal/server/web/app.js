(function () {
  "use strict";
  var $ = function (s) { return document.querySelector(s); };
  var cfg = null, locked = [];

  function api(method, path, body) {
    return fetch(path, {
      method: method,
      headers: body ? { "Content-Type": "application/json" } : {},
      body: body ? JSON.stringify(body) : undefined
    }).then(function (r) { return r.json(); });
  }
  function get(o, k) { return k.split(".").reduce(function (a, p) { return a == null ? a : a[p]; }, o); }
  function set(o, k, v) {
    var ps = k.split("."), last = ps.pop();
    ps.forEach(function (p) { o = o[p] = o[p] || {}; });
    o[last] = v;
  }
  function esc(s) { return String(s == null ? "" : s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; }); }

  function fillPrinters(list, def) {
    document.querySelectorAll("select.psel").forEach(function (sel) {
      var cur = sel.dataset.k ? get(cfg, sel.dataset.k) : sel.value;
      var html = '<option value="">' + (sel.id === "tpprinter" ? "(sesuai pengaturan)" : "(default Windows" + (def ? ": " + esc(def) : "") + ")") + "</option>";
      var names = list.map(function (p) { return p.name; });
      if (cur && names.indexOf(cur) < 0) names.push(cur);
      names.forEach(function (n) { html += "<option>" + esc(n) + "</option>"; });
      sel.innerHTML = html;
      sel.value = cur || "";
    });
  }

  function render() {
    document.querySelectorAll("[data-k]").forEach(function (el) {
      var k = el.dataset.k, v = get(cfg, k), t = el.dataset.type;
      if (t === "bool") el.checked = !!v;
      else if (t === "lines") el.value = (v || []).join("\n");
      else el.value = v == null ? "" : v;
      var isLocked = locked.indexOf(k) >= 0;
      el.disabled = isLocked;
      el.closest("label").classList.toggle("locked", isLocked);
      if (isLocked) el.closest("label").title = "Dikunci oleh developer";
    });
  }

  function collect() {
    var out = {};
    document.querySelectorAll("[data-k]").forEach(function (el) {
      var k = el.dataset.k, t = el.dataset.type, v;
      if (locked.indexOf(k) >= 0) return;
      if (t === "bool") v = el.checked;
      else if (t === "int") v = parseInt(el.value, 10) || 0;
      else if (t === "lines") v = el.value.split(/\r?\n/).map(function (s) { return s.trim(); }).filter(Boolean);
      else v = el.value.trim();
      set(out, k, v);
    });
    return out;
  }

  function loadConfig() {
    return api("GET", "/config").then(function (r) {
      cfg = r.config; locked = r.locked_keys || [];
      render();
      return api("GET", "/printers");
    }).then(function (r) { fillPrinters(r.printers || [], r.default); });
  }

  function loadStatus() {
    api("GET", "/status").then(function (s) {
      $("#ver").textContent = "Versi " + s.version + " · port " + s.port + " · " + s.os;
      $("#conn").textContent = "Aktif"; $("#conn").className = "pill ok";
      $("#paths").innerHTML = "Config: <code>" + esc(s.config_path) + "</code><br>Log: <code>" + esc(s.log_path) + "</code>";
      showUpdate(s.update);
    }).catch(function () { $("#conn").textContent = "Tidak terhubung"; $("#conn").className = "pill bad"; });
  }

  function showUpdate(u) {
    if (!u) return;
    var b = $("#updbanner");
    if (u.available && u.latest) {
      b.innerHTML = "Update tersedia: <b>v" + esc(u.latest.version) + "</b> " + esc(u.latest.notes || "") + ' <button id="updnow">Update sekarang</button>';
      b.classList.remove("hidden");
      $("#updnow").onclick = install;
      $("#updinstall").classList.remove("hidden");
    } else { b.classList.add("hidden"); $("#updinstall").classList.add("hidden"); }
    var t = u.last_check && u.last_check.indexOf("0001") !== 0 ? "Terakhir cek: " + new Date(u.last_check).toLocaleString("id-ID") : "Belum pernah cek";
    if (u.last_error) t += " · Error: " + u.last_error;
    if (!u.key_embedded) t += " · (public key update tidak tertanam: update dinonaktifkan)";
    $("#updres").textContent = t;
  }

  function install() {
    if (!confirm("Download dan pasang update sekarang? Aplikasi akan restart.")) return;
    $("#updres").textContent = "Mengunduh & memverifikasi update…";
    api("POST", "/update/install").then(function (r) {
      $("#updres").textContent = r.ok ? r.message : "Gagal: " + r.error;
    });
  }

  function loadJobs() {
    api("GET", "/jobs").then(function (r) {
      $("#jobs").innerHTML = (r.jobs || []).map(function (j) {
        return "<tr><td>" + new Date(j.created).toLocaleString("id-ID") + "</td><td>" + esc(j.type) + "</td><td>" + esc(j.printer) +
          "</td><td>" + esc(j.jobName) + '</td><td class="st-' + j.status + '">' + esc(j.status) + (j.error ? ": " + esc(j.error) : "") + "</td></tr>";
      }).join("") || '<tr><td colspan="5" class="hint">Belum ada job.</td></tr>';
    });
  }
  function loadLog() { api("GET", "/log?lines=150").then(function (r) { var p = $("#log"); p.textContent = (r.lines || []).join("\n"); p.scrollTop = p.scrollHeight; }); }

  $("#save").onclick = function () {
    $("#saveres").textContent = "Menyimpan…";
    api("PUT", "/config", collect()).then(function (r) {
      if (!r.ok) { $("#saveres").textContent = "Gagal: " + r.error; return; }
      cfg = r.config; render();
      var msg = "Tersimpan ✓";
      if (r.ignored_locked && r.ignored_locked.length) msg += " (dikunci developer: " + r.ignored_locked.join(", ") + ")";
      $("#saveres").textContent = msg;
      if (r.new_url) { $("#saveres").textContent += " · Port berubah, membuka " + r.new_url; setTimeout(function () { location.href = r.new_url; }, 1500); }
    }).catch(function (e) { $("#saveres").textContent = "Gagal: " + e; });
  };
  $("#tpbtn").onclick = function () {
    $("#tpres").textContent = "Mencetak…";
    api("POST", "/test-print", { type: $("#tptype").value, printer: $("#tpprinter").value }).then(function (r) {
      $("#tpres").textContent = r.ok ? "Berhasil dikirim ke " + r.job.printer : "Gagal: " + (r.error || "");
      loadJobs();
    });
  };
  $("#updcheck").onclick = function () {
    $("#updres").textContent = "Mengecek…";
    api("POST", "/update/check").then(function (r) {
      showUpdate(r.update);
      if (r.ok && !r.update.available) $("#updres").textContent = "Sudah versi terbaru. " + $("#updres").textContent;
    });
  };
  $("#updinstall").onclick = install;
  $("#jobsref").onclick = loadJobs;
  $("#logref").onclick = loadLog;

  // live job updates via WebSocket
  function ws() {
    var s = new WebSocket("ws://" + location.host + "/ws");
    s.onmessage = function (e) { var m = JSON.parse(e.data); if (m.type === "job") loadJobs(); if (m.type === "update") showUpdate(m.update); };
    s.onclose = function () { setTimeout(ws, 3000); };
  }

  loadConfig(); loadStatus(); loadJobs(); loadLog(); ws();
  setInterval(loadStatus, 15000);
})();
