let T = localStorage.getItem('token') || '';
let ME = {};
let LEADS = [], VEHS = [], CONVS = [], USERS = [], BOOKINGS = [];
let LABEL2ID = {};
let curConv = null, convTimer = null, qrTimer = null;

if (T) { document.getElementById('login').classList.add('hidden'); document.getElementById('app').classList.remove('hidden'); boot(); }

/* ---------- helpers ---------- */
function H(extra) { return Object.assign({'Content-Type': 'application/json', 'Authorization': 'Bearer ' + T}, extra || {}); }
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return {'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c]; }); }
function val(id) { return document.getElementById(id).value.trim(); }
function toast(msg, cls) {
  const d = document.createElement('div'); d.className = 'toast' + (cls === 'err' ? ' err' : ''); d.textContent = msg;
  document.getElementById('toast').appendChild(d); setTimeout(function () { d.remove(); }, 3500);
}
async function api(method, path, body) {
  const o = {method: method, headers: H()};
  if (body !== undefined) o.body = JSON.stringify(body);
  try {
    const r = await fetch(path, o);
    let j = null; try { j = await r.json(); } catch (e) {}
    if (!r.ok) toast((j && j.error) || ('Request failed (' + r.status + ')'), 'err');
    return {ok: r.ok, status: r.status, data: j};
  } catch (e) { toast('Network error', 'err'); return {ok: false, status: -1, data: null}; }
}
function fmtDate(s) {
  if (!s) return '—';
  const d = new Date(s); if (isNaN(d)) return String(s);
  return d.toLocaleString('en-IN', {day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit', hour12: true});
}
function fmtMoney(n) {
  n = +n || 0;
  if (n >= 1e7) return '₹' + (n / 1e7).toFixed(2).replace(/\.00$/, '') + ' Cr';
  if (n >= 1e5) return '₹' + (n / 1e5).toFixed(2).replace(/\.00$/, '') + ' L';
  if (n >= 1e3) return '₹' + (n / 1e3).toFixed(1).replace(/\.0$/, '') + 'k';
  return '₹' + n;
}
const PILLMAP = {AVAILABLE: 'green', DELIVERED: 'green', COMPLETED: 'green', CONVERTED: 'green', INTERESTED: 'green', SENT: 'green',
  RESERVED: 'amber', PENDING: 'amber', VALUATION_PENDING: 'amber', THINKING: 'amber', FOLLOWUP: 'amber', TEST_DRIVE: 'amber', SCHEDULED: 'amber',
  BOOKED: 'blue', CONFIRMED: 'blue', NEW: 'blue', CONTACTED: 'blue', QUALIFIED: 'blue', BUY: 'blue', SELL: 'blue', EXCHANGE: 'blue',
  SOLD: 'gray', LOST: 'gray', NOT_INTERESTED: 'gray', CANCELLED: 'gray', DONE: 'gray', FAILED_PERMANENTLY: 'red', FAILED: 'red'};
function pill(v) {
  v = v || '—';
  const c = PILLMAP[String(v).toUpperCase()] || 'gray';
  return '<span class="pill p-' + c + '">' + esc(v) + '</span>';
}
function sid(id) { return '<span class="id" title="' + esc(id) + '" onclick="navigator.clipboard&&navigator.clipboard.writeText(\'' + esc(id) + '\');toast(\'ID copied\')">' + esc(String(id || '').slice(0, 8)) + '</span>'; }
function login() {
  const email = val('email'), password = document.getElementById('password').value;
  fetch('/api/auth/login', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({email: email, password: password})})
    .then(function (r) { return r.json().then(function (j) { return {ok: r.ok, j: j}; }); })
    .then(function (x) {
      if (!x.ok) { document.getElementById('loginErr').textContent = (x.j && x.j.error) || 'Login failed'; return; }
      T = x.j.token; localStorage.setItem('token', T);
      document.getElementById('login').classList.add('hidden'); document.getElementById('app').classList.remove('hidden'); boot();
    });
}
function logout() { localStorage.removeItem('token'); location.reload(); }

/* ---------- nav ---------- */
const NAV = [['dash', 'Dashboard'], ['wa', 'WhatsApp'], ['leads', 'Leads'], ['conv', 'Conversations'], ['veh', 'Vehicles'],
  ['td', 'Test Drives'], ['fu', 'Follow-ups'], ['bk', 'Bookings'], ['neg', 'Negotiations'], ['fin', 'Finance'],
  ['sell', 'Sell Requests'], ['rev', 'Reviews'], ['team', 'Team'], ['sim', 'Simulator']];
function buildNav() {
  document.getElementById('nav').innerHTML = NAV.filter(function (n) { return n[0] !== 'team' || ME.role === 'admin'; })
    .map(function (n) { return '<button id="nav-' + n[0] + '" onclick="show(\'' + n[0] + '\')"><span class="t">' + n[1] + '</span><span class="n" id="badge-' + n[0] + '"></span></button>'; }).join('');
}
function show(k) {
  document.querySelectorAll('section').forEach(function (s) { s.classList.remove('active'); });
  document.getElementById('s-' + k).classList.add('active');
  document.querySelectorAll('#nav button').forEach(function (b) { b.classList.remove('on'); });
  const nb = document.getElementById('nav-' + k); if (nb) nb.classList.add('on');
  if (k === 'conv' && curConv) openConv(curConv);
}

/* ---------- boot ---------- */
async function boot() {
  const me = await api('GET', '/api/auth/me');
  if (!me.ok) { logout(); return; }
  ME = me.data;
  document.getElementById('meEmail').textContent = ME.email + ' (' + ME.role + ')';
  buildNav(); show('dash');
  await refreshHealth(); setInterval(refreshHealth, 30000);
  await reloadLookups();
  const st = document.getElementById('f_lead_status');
  ['NEW', 'CONTACTED', 'QUALIFIED', 'TEST_DRIVE', 'FOLLOWUP', 'BOOKED', 'CONVERTED', 'LOST'].forEach(function (s) {
    const o = document.createElement('option'); o.textContent = s; st.appendChild(o);
  });
  dash(); waStatus(); loadLeads(); loadConv(); renderVeh(); loadTD(); loadFU(); loadBK(); loadNG(); loadFIN(); loadSELL(); loadRV(); loadUsers();
}
async function reloadLookups() {
  const l = await api('GET', '/api/leads?limit=200'); LEADS = l.ok ? l.data : [];
  const v = await api('GET', '/api/vehicles?limit=200'); VEHS = v.ok ? v.data : [];
  const b = await api('GET', '/api/bookings'); BOOKINGS = b.ok ? b.data : [];
  if (ME.role === 'admin') { const u = await api('GET', '/api/users'); USERS = u.ok ? u.data : []; }
  LABEL2ID = {};
  const mk = function (items, label) {
    return items.map(function (o) {
      const key = label(o) + '  [' + String(o.id).slice(0, 8) + ']';
      LABEL2ID[key] = o.id; return '<option value="' + esc(key) + '">';
    }).join('');
  };
  document.getElementById('dl_leads').innerHTML = mk(LEADS, function (o) { return o.customer + ' — ' + o.phone; });
  document.getElementById('dl_veh').innerHTML = mk(VEHS, function (o) { return o.make + ' ' + o.model + ' ' + o.year + ' ' + fmtMoney(o.price); });
  document.getElementById('dl_bk').innerHTML = BOOKINGS.map(function (o) {
    const key = o.phone + ' — ' + o.vehicle + '  [' + String(o.id).slice(0, 8) + ']';
    LABEL2ID[key] = o.id; return '<option value="' + esc(key) + '">';
  }).join('');
}
function resolveId(inputId) {
  const v = val(inputId);
  if (!v) return '';
  if (LABEL2ID[v]) return LABEL2ID[v];
  if (/^[0-9a-f-]{36}$/i.test(v)) return v;
  toast('Pick a value from the suggestions', 'err'); return '';
}
async function refreshHealth() {
  try {
    const r = await fetch('/api/health'); const j = await r.json();
    document.getElementById('hDb').innerHTML = pill(j.db ? 'connected' : 'DOWN').replace('connected', 'DB ✓').replace('DOWN', 'DB ✗');
    const w = (j.whatsapp && j.whatsapp.status) || '?';
    document.getElementById('hWa').innerHTML = '<span class="pill ' + (w === 'connected' ? 'p-green' : w === 'qr' ? 'p-amber' : 'p-gray') + '">WA: ' + esc(w) + '</span>';
    document.getElementById('hDisk').textContent = j.disk_used_pct >= 0 ? 'disk ' + j.disk_used_pct + '%' : '';
  } catch (e) {}
}

/* ---------- dashboard ---------- */
async function dash() {
  const r = await api('GET', '/api/dashboard'); if (!r.ok) return;
  const j = r.data;
  const cards = [['leads', 'Leads', 'leads'], ['available_vehicles', 'Vehicles', 'veh'], ['scheduled_test_drives', 'Test drives', 'td'], ['pending_followups', 'Follow-ups', 'fu'], ['open_bookings', 'Bookings', 'bk']];
  document.getElementById('cards').innerHTML = cards.map(function (c) {
    return '<div class="card" onclick="show(\'' + c[2] + '\')"><div class="l">' + c[1] + '</div><b>' + (j[c[0]] || 0) + '</b></div>';
  }).join('');
  const fu = await api('GET', '/api/followups'); const sell = await api('GET', '/api/sell-requests'); const td = await api('GET', '/api/test-drives');
  let att = '';
  const pend = (fu.ok ? fu.data : []).filter(function (f) { return f.status === 'pending'; }).slice(0, 5);
  const val = (sell.ok ? sell.data : []).filter(function (s) { return s.status === 'VALUATION_PENDING'; });
  const upcoming = (td.ok ? td.data : []).filter(function (t) { return t.status === 'SCHEDULED'; }).slice(0, 5);
  document.getElementById('badge-fu').textContent = pend.length || '';
  document.getElementById('badge-sell').textContent = val.length || '';
  document.getElementById('badge-leads').textContent = j.leads || '';
  if (!pend.length && !val.length && !upcoming.length) att = '<div class="empty">All clear — nothing waiting.</div>';
  att += pend.map(function (f) { return '<div>• Follow-up for <b>' + esc(f.phone) + '</b> — ' + esc(f.type) + ' <span class="muted small">' + fmtDate(f.scheduled_at) + '</span></div>'; }).join('');
  att += val.map(function (s) { return '<div>• Valuation: <b>' + esc(s.brand) + ' ' + esc(s.model) + '</b> (' + esc(s.phone) + ')</div>'; }).join('');
  att += upcoming.map(function (t) { return '<div>• Test drive: <b>' + esc(t.vehicle) + '</b> (' + esc(t.phone) + ') <span class="muted small">' + fmtDate(t.scheduled_at) + '</span></div>'; }).join('');
  document.getElementById('attention').innerHTML = att;
  const c = await api('GET', '/api/conversations?limit=5');
  document.getElementById('recentConv').innerHTML = c.ok ? c.data.map(function (x) {
    return '<div><b>' + esc(x.name) + '</b> <span class="muted small">' + esc(x.phone) + ' · ' + x.messages + ' msgs</span></div>';
  }).join('') : '';
}

/* ---------- whatsapp ---------- */
async function waStatus() {
  const r = await api('GET', '/api/whatsapp/status'); if (!r.ok) return;
  const j = r.data;
  const st = j.status;
  document.getElementById('waCard').innerHTML = '<div class="panel">Status: ' +
    '<span class="pill ' + (st === 'connected' ? 'p-green' : st === 'qr' ? 'p-amber' : 'p-gray') + '">' + esc(st) + '</span>' +
    (j.jid ? ' <span class="muted small">' + esc(j.jid) + '</span>' : '') + '</div>';
  const w = document.getElementById('qrWrap');
  if (qrTimer) { clearTimeout(qrTimer); qrTimer = null; }
  if (j.has_qr) { w.innerHTML = '<p>Scan with WhatsApp → Linked devices:</p><img class="qr" id="qrImg" />'; loadQR();
    qrTimer = setTimeout(function () { if (document.getElementById('s-wa').classList.contains('active')) waStatus(); }, 20000);
  } else w.innerHTML = st === 'connected' ? '<p class="muted">Paired and receiving. New messages appear under Conversations.</p>' : '';
}
async function loadQR() {
  try {
    const r = await fetch('/api/whatsapp/qr', {headers: H()});
    if (!r.ok) return;
    const b = await r.blob(); const img = document.getElementById('qrImg');
    if (img) img.src = URL.createObjectURL(b);
  } catch (e) {}
}
async function waLogout() { if (!confirm('Unlink WhatsApp? You will need to scan again.')) return; await api('POST', '/api/whatsapp/logout'); waStatus(); }

/* ---------- leads ---------- */
async function loadLeads() {
  const r = await api('GET', '/api/leads?limit=100'); if (!r.ok) return;
  LEADS = r.data;
  const q = val('q_leads').toLowerCase(), fs = val('f_lead_status'), fi = val('f_lead_intent');
  const rows = LEADS.filter(function (o) {
    return (!q || (o.customer + ' ' + o.phone).toLowerCase().includes(q)) && (!fs || o.status === fs) && (!fi || o.intent === fi);
  });
  const salesOpts = USERS.filter(function (u) { return u.role === 'sales'; })
    .map(function (u) { return '<option value="' + u.id + '">' + esc(u.email) + '</option>'; }).join('');
  document.getElementById('leads').innerHTML = rows.length ? '<table><tr><th>Customer</th><th>Intent</th><th>Status</th><th>Interest</th><th>Source</th><th>Assign</th><th></th></tr>' +
    rows.map(function (o) {
      return '<tr><td><b>' + esc(o.customer) + '</b><br/><span class="muted small">' + esc(o.phone) + '</span><br/>' + sid(o.id) + '</td>' +
        '<td>' + pill(o.intent) + '<br/><span class="muted small">' + esc(o.state) + '</span></td>' +
        '<td>' + pill(o.status) + '</td><td>' + (o.interest ? pill(o.interest) : '<span class="muted">—</span>') + '</td>' +
        '<td class="muted small">' + esc(o.source) + '</td>' +
        '<td><select class="small" onchange="assignLead(\'' + o.id + '\',this.value)"><option value="">—</option>' + salesOpts + '</select><br/>' +
        '<button class="small" onclick="setInterest(\'' + o.id + '\',\'INTERESTED\')">Interested</button> ' +
        '<button class="small" onclick="setInterest(\'' + o.id + '\',\'THINKING\')">Thinking</button> ' +
        '<button class="small" onclick="setInterest(\'' + o.id + '\',\'NOT_INTERESTED\')">Lost</button></td>' +
        '<td><button class="small" onclick="openLeadChat(\'' + o.id + '\')">Chat</button></td></tr>';
    }).join('') + '</table>' : '<div class="empty">No leads yet — they appear when someone messages on WhatsApp.</div>';
}
async function assignLead(id, userId) {
  if (!userId) return;
  const r = await api('PATCH', '/api/leads/' + id + '/assign', {sales_user_id: userId});
  if (r.ok) { toast('Assigned'); loadLeads(); }
}
async function setInterest(id, v) {
  const r = await api('PATCH', '/api/leads/' + id + '/interest', {interest: v});
  if (r.ok) { toast('Marked ' + v); loadLeads(); }
}
function openLeadChat(leadId) {
  const c = CONVS.find(function (x) { return x.lead_id === leadId; });
  show('conv');
  if (c) openConv(c.id); else { loadConv().then(function () { const d = CONVS.find(function (x) { return x.lead_id === leadId; }); if (d) openConv(d.id); }); }
}

/* ---------- conversations ---------- */
async function loadConv() {
  const r = await api('GET', '/api/conversations?limit=50'); if (!r.ok) return;
  CONVS = r.data; renderConvList();
}
function renderConvList() {
  const q = val('q_conv').toLowerCase();
  const rows = CONVS.filter(function (c) { return !q || (c.name + ' ' + c.phone).toLowerCase().includes(q); });
  document.getElementById('convList').innerHTML = rows.length ? rows.map(function (c) {
    return '<div class="convitem' + (curConv === c.id ? ' on' : '') + '" onclick="openConv(\'' + c.id + '\')">' +
      '<div class="nm">' + esc(c.name || c.phone) + '</div><div class="ph">' + esc(c.phone) + ' · ' + c.messages + ' msgs · ' + fmtDate(c.updated_at) + '</div></div>';
  }).join('') : '<div class="empty">No conversations.</div>';
}
async function openConv(id) {
  curConv = id; renderConvList();
  if (convTimer) clearInterval(convTimer);
  await renderThread();
  convTimer = setInterval(function () { if (curConv === id) renderThread(true); }, 10000);
}
async function renderThread(quiet) {
  const c = CONVS.find(function (x) { return x.id === curConv; });
  if (!c) return;
  const r = await api('GET', '/api/messages?conversation_id=' + curConv);
  const msgs = r.ok ? r.data : [];
  document.getElementById('threadHead').innerHTML = '<b>' + esc(c.name || c.phone) + '</b> <span class="muted small">' + esc(c.phone) + '</span> ' +
    '<span style="float:right"><button class="small" id="botBtn" onclick="toggleBot(\'' + c.id + '\')">Pause bot</button></span>';
  const box = document.getElementById('thread');
  const nearBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 120;
  box.innerHTML = msgs.map(function (m) {
    let inner = esc(m.body);
    if (m.media) inner += '<br/><a href="/' + esc(m.media) + '" target="_blank"><img src="/' + esc(m.media) + '" loading="lazy"/></a>';
    return '<div class="bub ' + (m.direction === 'in' ? 'in' : 'out') + '">' + inner + '<span class="ts">' + fmtDate(m.created_at) + '</span></div>';
  }).join('');
  if (!quiet || nearBottom) box.scrollTop = box.scrollHeight;
}
async function sendReply() {
  const c = CONVS.find(function (x) { return x.id === curConv; });
  const box = document.getElementById('replyBox'); const text = box.value.trim();
  if (!c || !text) return;
  box.value = '';
  const r = await api('POST', '/api/whatsapp/send', {phone: c.phone, message: text});
  if (r.ok) { toast('Sent'); renderThread(); }
}
async function toggleBot(id) {
  const b = document.getElementById('botBtn');
  const off = b && b.textContent.includes('Pause');
  const r = await api('PATCH', '/api/conversations?id=' + id, {bot_enabled: !off});
  if (r.ok) { toast(off ? 'Bot paused — you reply manually' : 'Bot resumed'); if (b) b.textContent = off ? 'Resume bot' : 'Pause bot'; }
}

/* ---------- vehicles ---------- */
function renderVeh() {
  const q = val('q_veh').toLowerCase(), fs = val('f_veh_status');
  const rows = VEHS.filter(function (o) {
    return (!q || (o.make + ' ' + o.model).toLowerCase().includes(q)) && (!fs || o.status === fs);
  });
  document.getElementById('veh').innerHTML = rows.length ? rows.map(function (o) {
    const imgs = o.images || [];
    let acts = '';
    if (o.status === 'DRAFT') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'AVAILABLE\')">List</button>';
    if (o.status === 'AVAILABLE') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'RESERVED\')">Reserve</button> <button class="small" onclick="vehStatus(\'' + o.id + '\',\'SOLD\')">Mark sold</button>';
    if (o.status === 'RESERVED') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'AVAILABLE\')">Release</button> <button class="small" onclick="vehStatus(\'' + o.id + '\',\'SOLD\')">Mark sold</button>';
    if (o.status === 'BOOKED') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'DELIVERED\')">Delivered</button> <button class="small" onclick="vehStatus(\'' + o.id + '\',\'AVAILABLE\')">Release</button>';
    return '<div class="vcard">' + (imgs.length ? '<img src="' + esc(imgs[0]) + '" loading="lazy"/>' : '') +
      '<div class="b"><div class="t">' + esc(o.make) + ' ' + esc(o.model) + ' ' + esc(o.year) + '</div>' +
      '<div class="spec">' + esc(o.fuel) + ' · ' + esc(o.transmission) + ' · ' + Number(o.km || 0).toLocaleString('en-IN') + ' km</div>' +
      '<div class="price">' + fmtMoney(o.price) + '</div>' + pill(o.status) + ' ' + sid(o.id) +
      '<div class="thumbs">' + imgs.map(function (p) { return '<a href="' + esc(p) + '" target="_blank"><img src="' + esc(p) + '" loading="lazy"/></a>'; }).join('') + '</div>' +
      '<div class="acts">' + acts + ' <label class="small" style="cursor:pointer;border:1px solid #333a47;border-radius:8px;padding:4px 8px">+ Photos<input type="file" accept="image/*" multiple style="display:none" onchange="uploadVeh(\'' + o.id + '\',this)"/></label></div>' +
      '</div></div>';
  }).join('') : '<div class="empty">No vehicles — add your first car above.</div>';
}
async function loadVeh() { const r = await api('GET', '/api/vehicles?limit=100'); if (r.ok) { VEHS = r.data; renderVeh(); } }
async function addVehicle() {
  const g = function (id) { return val(id); };
  const body = {make: g('v_make'), model: g('v_model'), year: +g('v_year') || 0, price: +g('v_price') || 0, fuel: g('v_fuel'), transmission: g('v_trans'), km: +g('v_km') || 0, description: g('v_desc')};
  if (!body.make || !body.model) { toast('Make and model are required', 'err'); return; }
  const r = await api('POST', '/api/vehicles', body);
  if (r.ok) { toast('Vehicle added'); ['v_make', 'v_model', 'v_year', 'v_price', 'v_fuel', 'v_trans', 'v_km', 'v_desc'].forEach(function (i) { document.getElementById(i).value = ''; }); loadVeh(); }
}
async function vehStatus(id, st) {
  const r = await api('PATCH', '/api/vehicles/' + id, {status: st});
  if (r.ok) { toast('Marked ' + st); loadVeh(); }
}
async function uploadVeh(id, input) {
  const files = input.files; if (!files.length) return;
  let ok = 0, dup = 0;
  for (const f of files) {
    const fd = new FormData(); fd.append('file', f);
    try {
      const r = await fetch('/api/vehicles/' + id + '/images', {method: 'POST', headers: {Authorization: 'Bearer ' + T}, body: fd});
      const j = await r.json().catch(function () { return {}; });
      if (r.ok) { ok++; if (j.duplicate) dup++; }
      else toast((j && j.error) || 'Upload failed', 'err');
    } catch (e) { toast('Upload failed', 'err'); }
  }
  input.value = '';
  toast('Uploaded ' + ok + (dup ? ' (' + dup + ' already existed)' : ''));
  loadVeh();
}

/* ---------- test drives / followups / bookings ---------- */
function dtLocal(id) { const v = val(id); if (!v) return ''; return new Date(v).toISOString(); }
async function loadTD() {
  const r = await api('GET', '/api/test-drives'); if (!r.ok) return;
  document.getElementById('td').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Vehicle</th><th>When</th><th>Status</th></tr>' +
    r.data.map(function (o) { return '<tr><td>' + esc(o.phone) + '</td><td>' + esc(o.vehicle) + '</td><td>' + fmtDate(o.scheduled_at) + '</td><td>' + pill(o.status) + '</td></tr>'; }).join('') + '</table>' : '<div class="empty">No test drives scheduled.</div>';
}
async function addTD() {
  const lead = resolveId('td_lead'), veh = resolveId('td_veh'), when = dtLocal('td_when');
  if (!lead || !veh || !when) { if (!when) toast('Pick a date and time', 'err'); return; }
  const r = await api('POST', '/api/test-drives', {lead_id: lead, vehicle_id: veh, scheduled_at: when});
  if (r.ok) { toast('Test drive scheduled'); loadTD(); }
}
async function loadFU() {
  const r = await api('GET', '/api/followups'); if (!r.ok) return;
  document.getElementById('fu').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Type</th><th>When</th><th>Status</th><th>Message</th><th></th></tr>' +
    r.data.map(function (o) {
      const act = o.status === 'pending' ? '<button class="small" onclick="fuStatus(\'' + o.id + '\',\'cancelled\')">Cancel</button>' : '';
      return '<tr><td>' + esc(o.phone) + '</td><td>' + esc(o.type) + '</td><td>' + fmtDate(o.scheduled_at) + '</td><td>' + pill(o.status) + '</td><td>' + esc(o.message) + '</td><td>' + act + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No follow-ups.</div>';
}
async function addFU() {
  const lead = resolveId('fu_lead'); if (!lead) return;
  const when = dtLocal('fu_when'); if (!when) { toast('Pick a date and time', 'err'); return; }
  const r = await api('POST', '/api/followups', {lead_id: lead, type: val('fu_type') || 'general', scheduled_at: when, message: val('fu_msg')});
  if (r.ok) { toast('Follow-up added'); loadFU(); }
}
async function fuStatus(id, st) { const r = await api('PATCH', '/api/followups/' + id, {status: st}); if (r.ok) { toast('Updated'); loadFU(); } }
async function loadBK() {
  const r = await api('GET', '/api/bookings'); if (!r.ok) return;
  BOOKINGS = r.data;
  document.getElementById('bk').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Vehicle</th><th>Amount</th><th>Status</th><th></th></tr>' +
    r.data.map(function (o) {
      return '<tr><td>' + esc(o.phone) + '</td><td>' + esc(o.vehicle) + '</td><td>' + fmtMoney(o.amount) + '</td><td>' + pill(o.status) + '</td>' +
        '<td><button class="small" onclick="bkStatus(\'' + o.id + '\',\'CONFIRMED\')">Confirm</button> ' +
        '<button class="small" onclick="bkStatus(\'' + o.id + '\',\'COMPLETED\')">Delivered</button> ' +
        '<button class="small" onclick="bkStatus(\'' + o.id + '\',\'CANCELLED\')">Cancel</button></td></tr>';
    }).join('') + '</table>' : '<div class="empty">No bookings yet.</div>';
}
async function addBK() {
  const lead = resolveId('bk_lead'), veh = resolveId('bk_veh');
  if (!lead || !veh) return;
  const r = await api('POST', '/api/bookings', {lead_id: lead, vehicle_id: veh, amount: +val('bk_amt') || 0});
  if (r.ok) { toast('Booked'); loadBK(); }
}
async function bkStatus(id, st) {
  const msg = st === 'CANCELLED' ? 'Cancel this booking? The vehicle returns to AVAILABLE.' : 'Mark booking ' + st + '?';
  if (!confirm(msg)) return;
  const body = {status: st};
  if (st === 'COMPLETED') body.delivery_at = new Date().toISOString();
  const r = await api('PATCH', '/api/bookings/' + id, body);
  if (r.ok) { toast('Booking ' + st); loadBK(); }
}

/* ---------- negotiations / finance / sell / reviews / team ---------- */
async function loadNG() {
  const r = await api('GET', '/api/negotiations'); if (!r.ok) return;
  document.getElementById('neg').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Vehicle</th><th>Customer offer</th><th>Sales offer</th><th>Final</th></tr>' +
    r.data.map(function (o) { return '<tr><td>' + esc(o.phone) + '</td><td>' + esc(o.vehicle) + '</td><td>' + fmtMoney(o.customer_offer) + '</td><td>' + fmtMoney(o.sales_offer) + '</td><td><b>' + fmtMoney(o.final_price) + '</b></td></tr>'; }).join('') + '</table>' : '<div class="empty">No negotiations.</div>';
}
async function addNG() {
  const lead = resolveId('ng_lead'), veh = resolveId('ng_veh');
  if (!lead || !veh) return;
  const r = await api('POST', '/api/negotiations', {lead_id: lead, vehicle_id: veh, customer_offer: +val('ng_co') || 0, sales_offer: +val('ng_so') || 0, final_price: +val('ng_fp') || 0});
  if (r.ok) { toast('Saved'); loadNG(); }
}
async function loadFIN() {
  const r = await api('GET', '/api/finance'); if (!r.ok) return;
  document.getElementById('fin').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Loan</th><th>Tenure</th><th>Employment</th><th>Income</th><th>Status</th></tr>' +
    r.data.map(function (o) { return '<tr><td>' + esc(o.phone) + '</td><td>' + fmtMoney(o.loan_amount) + '</td><td>' + esc(o.tenure_months) + ' mo</td><td>' + esc(o.employment) + '</td><td>' + fmtMoney(o.income) + '</td><td>' + pill(o.status) + '</td></tr>'; }).join('') + '</table>' : '<div class="empty">No finance enquiries.</div>';
}
async function loadSELL() {
  const r = await api('GET', '/api/sell-requests'); if (!r.ok) return;
  document.getElementById('sell').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Car</th><th>Year</th><th>KM</th><th>Reg</th><th>Photos</th><th>Status</th></tr>' +
    r.data.map(function (o) { return '<tr><td>' + esc(o.phone) + '<br/>' + sid(o.id) + '</td><td><b>' + esc(o.brand) + ' ' + esc(o.model) + '</b><br/><span class="muted small">' + esc(o.fuel) + ' · ' + esc(o.transmission) + ' · ' + esc(o.condition) + ' · ' + esc(o.location) + '</span></td><td>' + esc(o.year) + '</td><td>' + Number(o.km || 0).toLocaleString('en-IN') + '</td><td class="small">' + esc(o.registration) + '</td><td>' + esc(o.photo_count) + '</td><td>' + pill(o.status) + '</td></tr>'; }).join('') + '</table>' : '<div class="empty">No sell requests.</div>';
}
async function loadRV() {
  const r = await api('GET', '/api/reviews'); if (!r.ok) return;
  document.getElementById('rev').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Rating</th><th>Review</th></tr>' +
    r.data.map(function (o) { return '<tr><td>' + esc(o.phone) + '</td><td>' + '★'.repeat(+o.rating || 0) + '</td><td>' + esc(o.review) + '</td></tr>'; }).join('') + '</table>' : '<div class="empty">No reviews yet.</div>';
}
async function addRV() {
  const bk = resolveId('rv_bk'); if (!bk) return;
  const rate = +val('rv_rate');
  if (rate < 1 || rate > 5) { toast('Rating must be 1–5', 'err'); return; }
  const r = await api('POST', '/api/reviews', {booking_id: bk, rating: rate, review: val('rv_txt')});
  if (r.ok) { toast('Review saved'); loadRV(); }
}
async function loadUsers() {
  if (ME.role !== 'admin') return;
  const r = await api('GET', '/api/users'); if (!r.ok) return;
  USERS = r.data;
  document.getElementById('users').innerHTML = '<table><tr><th>Email</th><th>Role</th><th>Since</th></tr>' +
    USERS.map(function (u) { return '<tr><td>' + esc(u.email) + '</td><td>' + pill(u.role) + '</td><td class="muted small">' + fmtDate(u.created_at) + '</td></tr>'; }).join('') + '</table>';
}
async function addUser() {
  const r = await api('POST', '/api/users', {email: val('u_email'), password: document.getElementById('u_pass').value, role: val('u_role')});
  if (r.ok) { toast('Member added'); document.getElementById('u_email').value = ''; document.getElementById('u_pass').value = ''; loadUsers(); }
}

/* ---------- simulator ---------- */
function simBubble(text, dir) {
  const box = document.getElementById('simThread');
  const d = document.createElement('div'); d.className = 'bub ' + dir; d.textContent = text;
  box.appendChild(d); box.scrollTop = box.scrollHeight;
}
async function sim() {
  const phone = val('sim_phone'), box = document.getElementById('sim_body');
  const body = box.value.trim(); if (!phone || !body) return;
  box.value = ''; simBubble(body, 'in');
  const r = await api('POST', '/api/whatsapp/simulate', {phone: phone, body: body});
  simBubble((r.data && r.data.reply) || '(no reply)', 'out');
}
