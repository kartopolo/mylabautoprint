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
      var html = '<option value="">' + (sel.id === "tpprinter" ? "(sesuai pengaturan)" : sel.id === "pf_printer" ? "(default per tipe / Windows)" : "(default Windows" + (def ? ": " + esc(def) : "") + ")") + "</option>";
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
          "</td><td>" + esc(j.jobName) + '</td><td class="st-' + j.status + '">' + esc(j.status) + (j.error ? ": " + esc(j.error) : "") + (j.warnings && j.warnings.length ? '<br><span class="hint">⚠ ' + esc(j.warnings.join("; ")) + "</span>" : "") + (j.profile ? '<br><span class="hint">profil: ' + esc(j.profile) + "</span>" : "") + "</td></tr>";
      }).join("") || '<tr><td colspan="5" class="hint">Belum ada job.</td></tr>';
    });
  }
  function loadLog() { api("GET", "/log?lines=150").then(function (r) { var p = $("#log"); p.textContent = (r.lines || []).join("\n"); p.scrollTop = p.scrollHeight; }); }

  // ---- Profil Printer -------------------------------------------------------
  var profiles = {}, editing = null;
  function loadProfiles() {
    return api("GET", "/profiles").then(function (r) { profiles = r.profiles || {}; renderProfiles(); });
  }
  function renderProfiles() {
    var names = Object.keys(profiles).sort();
    $("#proflist").innerHTML = names.map(function (n) {
      var p = profiles[n], extra = [];
      if (p.scale) extra.push(p.scale);
      if (p.color) extra.push(p.color === "mono" ? "hitam-putih" : "berwarna");
      if (p.duplex) extra.push("duplex " + p.duplex);
      if (p.tray) extra.push("tray " + p.tray);
      if (p.margin_mm != null) extra.push("margin " + p.margin_mm + "mm");
      if (p.cut) extra.push("cut");
      return "<tr><td><b>" + esc(n) + "</b></td><td>" + esc(p.printer || "(default)") + "</td><td>" + esc(p.paper || "-") +
        "</td><td>" + esc(p.orientation || "-") + "</td><td>" + esc(p.copies || "-") + "</td><td>" + esc(extra.join(", ")) +
        '</td><td><button class="link" data-edit="' + esc(n) + '">ubah</button> · <button class="link" data-del="' + esc(n) + '">hapus</button></td></tr>';
    }).join("") || '<tr><td colspan="7" class="hint">Belum ada profil.</td></tr>';
    document.querySelectorAll("[data-edit]").forEach(function (b) { b.onclick = function () { editProfile(b.dataset.edit); }; });
    document.querySelectorAll("[data-del]").forEach(function (b) {
      b.onclick = function () {
        if (!confirm("Hapus profil " + b.dataset.del + "?")) return;
        api("DELETE", "/profiles/" + encodeURIComponent(b.dataset.del)).then(function (r) { if (!r.ok) alert(r.error); loadProfiles(); });
      };
    });
  }
  function editProfile(name) {
    var p = (name && profiles[name]) || {};
    editing = name || null;
    $("#profform").classList.remove("hidden");
    $("#pf_name").value = name || ""; $("#pf_name").disabled = !!name;
    $("#pf_printer").value = p.printer || "";
    $("#pf_paper").value = p.paper || ""; $("#pf_orientation").value = p.orientation || "";
    $("#pf_copies").value = p.copies || ""; $("#pf_scale").value = p.scale || "";
    $("#pf_color").value = p.color || ""; $("#pf_duplex").value = p.duplex || "";
    $("#pf_tray").value = p.tray || ""; $("#pf_margin").value = p.margin_mm != null ? p.margin_mm : "";
    $("#pf_font").value = p.font_size || ""; $("#pf_cut").checked = !!p.cut;
    $("#pf_res").textContent = "";
    loadCaps();
    $("#profform").scrollIntoView({ behavior: "smooth" });
  }
  function loadCaps() {
    var printer = $("#pf_printer").value;
    $("#pf_papers").innerHTML = ["A4", "A5", "A6", "Letter", "Legal", "F4", "80mm", "58mm", "100x150mm"].map(function (x) { return '<option value="' + x + '">'; }).join("");
    $("#pf_bins").innerHTML = ""; $("#pf_caps").textContent = "";
    if (!printer) return;
    api("GET", "/printers/capabilities?name=" + encodeURIComponent(printer)).then(function (r) {
      if (!r.ok) { $("#pf_caps").textContent = "Kemampuan printer tidak terbaca: " + r.error; return; }
      var c = r.capabilities;
      $("#pf_papers").innerHTML += (c.papers || []).map(function (x) { return '<option value="' + esc(x.name) + '">' + esc(x.width_mm + "×" + x.height_mm + " mm") + "</option>"; }).join("");
      $("#pf_bins").innerHTML = (c.bins || []).map(function (x) { return '<option value="' + esc(x.name) + '">'; }).join("");
      $("#pf_caps").textContent = "Printer ini: " + (c.papers || []).length + " ukuran kertas, " + (c.bins || []).length + " tray, duplex " +
        (c.duplex ? "ya" : "tidak") + ", warna " + (c.color ? "ya" : "tidak") + ".";
    });
  }
  function collectProfile() {
    var p = { printer: $("#pf_printer").value };
    var str = { paper: "#pf_paper", orientation: "#pf_orientation", scale: "#pf_scale", color: "#pf_color", duplex: "#pf_duplex", tray: "#pf_tray" };
    Object.keys(str).forEach(function (k) { var v = $(str[k]).value.trim(); if (v) p[k] = v; });
    if (+$("#pf_copies").value > 0) p.copies = +$("#pf_copies").value;
    if ($("#pf_margin").value !== "") p.margin_mm = +$("#pf_margin").value;
    if (+$("#pf_font").value > 0) p.font_size = +$("#pf_font").value;
    if ($("#pf_cut").checked) p.cut = true;
    return p;
  }
  function saveProfile() {
    var name = (editing || $("#pf_name").value).trim();
    if (!name) { $("#pf_res").textContent = "Isi nama profil dulu."; return Promise.reject(); }
    return api("PUT", "/profiles/" + encodeURIComponent(name), collectProfile()).then(function (r) {
      if (!r.ok) { $("#pf_res").textContent = "Gagal: " + r.error; throw new Error(r.error); }
      editing = name; $("#pf_name").disabled = true;
      $("#pf_res").textContent = "Profil " + name + " tersimpan ✓";
      return loadProfiles().then(function () { return name; });
    });
  }
  $("#profnew").onclick = function () { editProfile(null); };
  $("#pf_cancel").onclick = function () { $("#profform").classList.add("hidden"); editing = null; };
  $("#pf_printer").onchange = loadCaps;
  $("#pf_save").onclick = function () { saveProfile().catch(function () {}); };
  $("#pf_test").onclick = function () {
    saveProfile().then(function (name) {
      $("#pf_res").textContent = "Mencetak dengan profil " + name + "…";
      return api("POST", "/test-print", { type: $("#pf_testtype").value, profile: name });
    }).then(function (r) {
      if (!r) return;
      var w = r.job && r.job.warnings && r.job.warnings.length ? " (peringatan: " + r.job.warnings.join("; ") + ")" : "";
      $("#pf_res").textContent = (r.ok ? "Berhasil dikirim ke " + r.job.printer : "Gagal: " + (r.error || "")) + w;
      loadJobs();
    }).catch(function () {});
  };

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
    s.onmessage = function (e) {
      var m = JSON.parse(e.data);
      if (m.type === "job") loadJobs();
      if (m.type === "update") showUpdate(m.update);
      if (m.type === "profiles") { profiles = m.profiles || {}; renderProfiles(); }
    };
    s.onclose = function () { setTimeout(ws, 3000); };
  }

  loadConfig().then(loadProfiles); loadStatus(); loadJobs(); loadLog(); ws();
  setInterval(loadStatus, 15000);
})();
