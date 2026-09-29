let T = localStorage.getItem('token') || '';
let ME = {};
let LEADS = [], VEHS = [], VEHCOUNT = null, CONVS = [], USERS = [], BOOKINGS = [];
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
  return d.toLocaleString('en-MY', {day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit', hour12: true});
}
function fmtMoney(n) {
  n = +n || 0;
  return 'RM ' + n.toLocaleString('en-MY');
}
const PILLMAP = {AVAILABLE: 'green', DELIVERED: 'green', COMPLETED: 'green', CONVERTED: 'green', INTERESTED: 'green', SENT: 'green', RECEIVED: 'green',
  RESERVED: 'amber', PENDING: 'amber', VALUATION_PENDING: 'amber', THINKING: 'amber', FOLLOWUP: 'amber', TEST_DRIVE: 'amber', SCHEDULED: 'amber', PARTIAL: 'amber',
  BOOKED: 'blue', CONFIRMED: 'blue', NEW: 'blue', CONTACTED: 'blue', QUALIFIED: 'blue', BUY: 'blue', SELL: 'blue',
  SOLD: 'gray', LOST: 'gray', NOT_INTERESTED: 'gray', CANCELLED: 'gray', DONE: 'gray', REFUNDED: 'gray', NO_SHOW: 'gray', FAILED_PERMANENTLY: 'red', FAILED: 'red'};
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
const NAV = [['dash', 'Dashboard'], ['guide', 'Guide'], ['wa', 'WhatsApp'], ['cust', 'Customers'], ['leads', 'Leads'], ['conv', 'Conversations'], ['veh', 'Vehicles'],
  ['td', 'Test Drives'], ['insp', 'Inspections'], ['fu', 'Follow-ups'], ['bk', 'Bookings'], ['pay', 'Payments'], ['neg', 'Negotiations'], ['fin', 'Finance'],
  ['sell', 'Sell Requests'], ['rev', 'Reviews'], ['bot', 'Bot Config'], ['team', 'Team'], ['set', 'Settings'], ['sim', 'Simulator']];
function buildNav() {
  document.getElementById('nav').innerHTML = NAV.filter(function (n) { return (n[0] !== 'team' && n[0] !== 'bot') || ME.role === 'admin'; })
    .map(function (n) { return '<button id="nav-' + n[0] + '" onclick="show(\'' + n[0] + '\')"><span class="t">' + n[1] + '</span><span class="n" id="badge-' + n[0] + '"></span></button>'; }).join('');
}
function show(k) {
  document.querySelectorAll('section').forEach(function (s) { s.classList.remove('active'); });
  document.getElementById('s-' + k).classList.add('active');
  document.querySelectorAll('#nav button').forEach(function (b) { b.classList.remove('on'); });
  const nb = document.getElementById('nav-' + k); if (nb) nb.classList.add('on');
  if (k === 'conv' && curConv) openConv(curConv);
  if (k === 'bot') {
    if (currentBotView === 'canvas') {
      initDrawflowIfNeeded();
      loadCanvasFlowsDropdown();
    } else {
      loadBotFlows();
    }
  }
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
  dash(); waStatus(); loadCust(); loadLeads(); loadConv(); renderVeh(); loadTD(); loadINSP(); loadFU(); loadBK(); loadPAY(); loadNG(); loadFIN(); loadSELL(); loadRV(); loadUsers(); loadSettings();
}
async function reloadLookups() {
  const l = await api('GET', '/api/leads?limit=200'); LEADS = l.ok ? l.data : [];
  const v = await api('GET', '/api/vehicles?limit=500'); VEHS = v.ok ? v.data : [];
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
    document.getElementById('hDisk').textContent = (j.disk_used_pct >= 0 ? 'disk ' + j.disk_used_pct + '%' : '') + (j.timezone ? ' · ' + j.timezone : '');
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
  const insp = await api('GET', '/api/inspections');
  let att = '';
  const pend = (fu.ok ? fu.data : []).filter(function (f) { return f.status === 'pending'; }).slice(0, 5);
  const val = (sell.ok ? sell.data : []).filter(function (s) { return s.status === 'VALUATION_PENDING'; });
  const upcoming = (td.ok ? td.data : []).filter(function (t) { return t.status === 'SCHEDULED'; }).slice(0, 5);
  const inspUp = (insp.ok ? insp.data : []).filter(function (t) { return t.status === 'SCHEDULED'; }).slice(0, 5);
  document.getElementById('badge-fu').textContent = pend.length || '';
  document.getElementById('badge-sell').textContent = val.length || '';
  document.getElementById('badge-insp').textContent = inspUp.length || '';
  document.getElementById('badge-leads').textContent = j.leads || '';
  if (!pend.length && !val.length && !upcoming.length && !inspUp.length) att = '<div class="empty">All clear — nothing waiting.</div>';
  att += pend.map(function (f) { return '<div>• Follow-up for <b>' + esc(f.phone) + '</b> — ' + esc(f.type) + ' <span class="muted small">' + fmtDate(f.scheduled_at) + '</span></div>'; }).join('');
  att += val.map(function (s) { return '<div>• Valuation: <b>' + esc(s.brand) + ' ' + esc(s.model) + '</b> (' + esc(s.phone) + ')</div>'; }).join('');
  att += upcoming.map(function (t) { return '<div>• Test drive: <b>' + esc(t.vehicle) + '</b> (' + esc(t.phone) + ') <span class="muted small">' + fmtDate(t.scheduled_at) + '</span></div>'; }).join('');
  att += inspUp.map(function (t) { return '<div>• Inspection: <b>' + esc(t.phone) + '</b> <span class="muted small">' + fmtDate(t.scheduled_at) + '</span></div>'; }).join('');
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
  const pillCls = st === 'connected' ? 'p-green' : (st === 'qr' ? 'p-amber' : (st === 'logged_out' ? 'p-red' : 'p-gray'));
  let extra = '';
  if (j.last_error) extra += '<div class="muted small" style="margin-top:6px">Last error (' + (j.fail_count || 0) + ' tries): ' + esc(j.last_error) + '</div>';
  if (st === 'logged_out') extra += '<div style="margin-top:8px"><b>Logged out by WhatsApp/phone.</b> The session is gone server-side — press <b>Reconnect</b> below, then scan the fresh QR. This is the only case that needs you; anything else recovers alone.</div>';
  else if (st === 'connecting' && (j.fail_count || 0) > 3) extra += '<div class="muted small" style="margin-top:6px">Retrying in the background — it recovers on its own. (Press <b>Reconnect</b> only to start a completely fresh pairing.)</div>';
  document.getElementById('waCard').innerHTML = '<div class="panel">Status: ' +
    '<span class="pill ' + pillCls + '">' + esc(st) + '</span>' +
    (j.jid ? ' <span class="muted small">' + esc(j.jid) + '</span>' : '') + extra + '</div>';
  const w = document.getElementById('qrWrap');
  if (qrTimer) { clearTimeout(qrTimer); qrTimer = null; }
  if (j.has_qr) { w.innerHTML = '<p>Scan with WhatsApp → Linked devices:</p><img class="qr" id="qrImg" />' +
    '<div class="muted small" style="margin-top:8px">No camera? Link with phone number instead:</div>' +
    '<div style="display:flex;gap:6px;margin-top:4px"><input id="pairPhone" placeholder="60123456789" style="flex:1" />' +
    '<button onclick="waPairCode()">Get code</button></div>' +
    '<div id="pairOut" style="margin-top:6px">' + (j.pair_code ? ('<b style="font-size:20px;letter-spacing:3px">' + esc(j.pair_code) + '</b><div class="muted small">Type into phone → Linked devices → Link with phone number. Expires ' + esc(j.pair_code_expires_at || '') + '</div>') : '') + '</div>';
    loadQR();
    qrTimer = setTimeout(function () { if (document.getElementById('s-wa').classList.contains('active')) waStatus(); }, 20000);
  } else if (st === 'logged_out') { w.innerHTML = '<p class="muted">Session ended — press <b>Reconnect</b> above to generate a fresh QR, then scan it.</p>'; }
  else w.innerHTML = st === 'connected' ? '<p class="muted">Paired and receiving. New messages appear under Conversations.</p>' : '';
}
async function loadQR() {
  try {
    const r = await fetch('/api/whatsapp/qr?t=' + Date.now(), {headers: H()});
    if (!r.ok) return;
    const b = await r.blob(); const img = document.getElementById('qrImg');
    if (img) img.src = URL.createObjectURL(b);
  } catch (e) {}
}
async function waPairCode() {
  const inp = document.getElementById('pairPhone');
  const phone = inp && inp.value.trim();
  if (!phone) { toast('Enter the phone number in international format', 'err'); return; }
  const r = await api('POST', '/api/whatsapp/pair-code', {phone: phone});
  const out = document.getElementById('pairOut');
  if (!r.ok || !out) return;
  if (r.data && r.data.code) {
    out.innerHTML = '<b style="font-size:20px;letter-spacing:3px">' + esc(r.data.code) + '</b><div class="muted small">Type into phone → Linked devices → Link with phone number. Expires ' + esc(r.data.expires_at || '') + '</div>';
  } else {
    out.innerHTML = '<div class="muted small">Code requested — it appears here in a few seconds…</div>';
    setTimeout(waStatus, 5000);
  }
}
async function waLogout() { if (!confirm('Unlink WhatsApp? You will need to scan again.')) return; await api('POST', '/api/whatsapp/logout'); waStatus(); }
async function waReconnect() {
  if (!confirm('Clear the saved session and start fresh pairing?')) return;
  const r = await api('POST', '/api/whatsapp/reconnect', {});
  if (r.ok) { toast('Session cleared — fresh QR coming'); setTimeout(waStatus, 3000); }
}

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
        '<td><button class="small" onclick="openLeadChat(\'' + o.id + '\')">Chat</button>' +
        (ME.role === 'admin' ? ' <button class="small danger" onclick="delLead(\'' + o.id + '\')">Delete</button>' : '') + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No leads yet — they appear when someone messages on WhatsApp.</div>';
}
/* ---------- customers ---------- */
async function loadCust() {
  const r = await api('GET', '/api/customers'); if (!r.ok) return;
  const q = val('q_cust').toLowerCase();
  const rows = r.data.filter(function (o) { return !q || (o.name + ' ' + o.phone).toLowerCase().includes(q); });
  document.getElementById('cust').innerHTML = rows.length ? '<table><tr><th>Name</th><th>Phone</th><th>Source</th><th>Since</th><th></th></tr>' +
    rows.map(function (o) {
      return '<tr><td><b>' + esc(o.name) + '</b><br/>' + sid(o.id) + '</td><td>' + esc(o.phone) + '</td>' +
        '<td class="muted small">' + esc(o.source) + '</td><td class="muted small">' + fmtDate(o.created_at) + '</td>' +
        '<td><button class="small" onclick="renameCust(\'' + o.id + '\',\'' + esc(o.name).replace(/'/g, "\\'") + '\')">Rename</button> ' +
        '<button class="small danger" onclick="delCust(\'' + o.id + '\',\'' + esc(o.name) + '\')">Delete</button></td></tr>';
    }).join('') + '</table>' : '<div class="empty">No customers yet.</div>';
}
async function renameCust(id, oldName) {
  const v = prompt('Customer name:', oldName || '');
  if (!v) return;
  const r = await api('PATCH', '/api/customers/' + id, {name: v});
  if (r.ok) { toast('Renamed'); loadCust(); }
}
async function delCust(id, name) {
  if (!confirm('Delete customer "' + name + '" and all their leads, chats and bookings?')) return;
  const r = await api('DELETE', '/api/customers/' + id);
  if (r.ok) { toast('Deleted'); loadCust(); }
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
    if (m.media) inner += '<br/><a href="/' + esc(m.media) + '" target="_blank"><img src="/' + esc(m.media) + '" loading="lazy" onerror="this.closest(\'a\').outerHTML=\'<span class=&quot;muted small&quot;>[photo unavailable — file was lost before backup]</span>\'"/></a>';
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
  const vc = document.getElementById('vehCount');
  if (vc) vc.textContent = VEHCOUNT && VEHCOUNT.total != null ? '· ' + VEHCOUNT.total + ' total (' + VEHS.length + ' shown)' : '(' + VEHS.length + ' shown)';
  document.getElementById('veh').innerHTML = rows.length ? rows.map(function (o) {
    const imgs = o.images || [];
    let acts = '';
    if (o.status === 'DRAFT') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'AVAILABLE\')">List</button>';
    if (o.status === 'AVAILABLE') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'RESERVED\')">Reserve</button> <button class="small" onclick="vehStatus(\'' + o.id + '\',\'SOLD\')">Mark sold</button>';
    if (o.status === 'RESERVED') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'AVAILABLE\')">Release</button> <button class="small" onclick="vehStatus(\'' + o.id + '\',\'SOLD\')">Mark sold</button>';
    if (o.status === 'BOOKED') acts = '<button class="small" onclick="vehStatus(\'' + o.id + '\',\'DELIVERED\')">Delivered</button> <button class="small" onclick="vehStatus(\'' + o.id + '\',\'AVAILABLE\')">Release</button>';
    const delBtn = ME.role === 'admin' ? ' <button class="small danger" onclick="delVeh(\'' + o.id + '\',\'' + esc(o.make + ' ' + o.model).replace(/'/g, "\\'") + '\')">Delete</button>' : '';
    return '<div class="vcard">' + (imgs.length ? '<img src="' + esc(imgs[0]) + '" loading="lazy" onerror="this.remove()"/>' : '') +
      '<div class="b"><div class="t">' + esc(o.make) + ' ' + esc(o.model) + ' ' + esc(o.year) + '</div>' +
      '<div class="spec">' + [o.fuel, o.transmission].filter(Boolean).join(' · ') + (o.fuel || o.transmission ? ' · ' : '') + Number(o.km || 0).toLocaleString('en-MY') + ' km</div>' +
      ((o.stock_no || o.reg_num || o.colour || o.stock_location) ? '<div class="spec">' + [o.stock_no, o.reg_num, o.colour, o.stock_location].filter(Boolean).map(esc).join(' · ') + '</div>' : '') +
      ((o.stock_status || o.warranty) ? '<div class="spec">' + [o.stock_status, o.warranty].filter(Boolean).map(esc).join(' · ') + '</div>' : '') +
      (o.acquired_via ? '<div class="spec">Via: ' + esc(o.acquired_via) + '</div>' : '') +
      (o.claims ? '<div class="spec">Note: ' + esc(o.claims) + '</div>' : '') +
      '<div class="price">' + fmtMoney(o.price) + '</div>' + pill(o.status) + ' ' + sid(o.id) +
      '<div class="thumbs">' + imgs.map(function (p) { return '<a href="' + esc(p) + '" target="_blank"><img src="' + esc(p) + '" loading="lazy" onerror="this.closest(\'a\').remove()"/></a>'; }).join('') + '</div>' +
      '<div class="acts">' + acts + delBtn + ' <label class="small" style="cursor:pointer;border:1px solid #c4cede;border-radius:8px;padding:4px 8px">+ Photos<input type="file" accept="image/*" multiple style="display:none" onchange="uploadVeh(\'' + o.id + '\',this)"/></label></div>' +
      '</div></div>';
  }).join('') : '<div class="empty">No vehicles — add your first car above.</div>';
}
async function loadVeh() { const r = await api('GET', '/api/vehicles?limit=500'); if (r.ok) { VEHS = r.data; renderVeh(); } const c = await api('GET', '/api/vehicles/count'); if (c.ok) { VEHCOUNT = c.data; renderVeh(); } }
async function addVehicle() {
  const g = function (id) { return val(id); };
  const body = {make: g('v_make'), model: g('v_model'), year: +g('v_year') || 0, price: +g('v_price') || 0, fuel: g('v_fuel'), transmission: g('v_trans'), km: +g('v_km') || 0, description: g('v_desc'), acquired_via: document.getElementById('v_acq').value};
  if (!body.make || !body.model) { toast('Make and model are required', 'err'); return; }
  const r = await api('POST', '/api/vehicles', body);
  if (r.ok) { toast('Vehicle added'); ['v_make', 'v_model', 'v_year', 'v_price', 'v_fuel', 'v_trans', 'v_km', 'v_desc'].forEach(function (i) { document.getElementById(i).value = ''; }); document.getElementById('v_acq').value = ''; loadVeh(); }
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
  document.getElementById('td').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Vehicle</th><th>When</th><th>Status</th><th></th></tr>' +
    r.data.map(function (o) {
      const act = o.status === 'SCHEDULED' ? '<button class="small" onclick="tdStatus(\'' + o.id + '\',\'COMPLETED\')">Done</button> <button class="small" onclick="tdStatus(\'' + o.id + '\',\'CANCELLED\')">Cancel</button> <button class="small" onclick="tdStatus(\'' + o.id + '\',\'NO_SHOW\')">No-show</button>' : '';
      return '<tr><td>' + esc(o.phone) + '</td><td>' + esc(o.vehicle) + '</td><td>' + fmtDate(o.scheduled_at) + '</td><td>' + pill(o.status) + '</td><td>' + act + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No test drives scheduled.</div>';
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
      let act = '';
      if (o.status === 'pending') act = '<button class="small" onclick="fuStatus(\'' + o.id + '\',\'cancelled\')">Cancel</button> ';
      if (ME.role === 'admin') act += '<button class="small danger" onclick="delFU(\'' + o.id + '\')">Delete</button>';
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

/* ---------- payments (manual log) / inspections (sell flow) ---------- */
async function loadPAY() {
  const r = await api('GET', '/api/payments'); if (!r.ok) return;
  document.getElementById('pay').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Vehicle</th><th>Amount</th><th>Method</th><th>Status</th><th>By</th><th></th></tr>' +
    r.data.map(function (o) {
      const open = o.status === 'PENDING' || o.status === 'PARTIAL';
      const act = open ? '<button class="small" onclick="payStatus(\'' + o.id + '\',\'RECEIVED\')">Received</button> ' +
        '<button class="small" onclick="payStatus(\'' + o.id + '\',\'REFUNDED\')">Refund</button>' : '';
      return '<tr><td>' + esc(o.phone) + '<br/>' + sid(o.id) + '</td><td>' + esc(o.vehicle) + '</td><td><b>' + fmtMoney(o.amount) + '</b>' +
        (o.notes ? '<br/><span class="muted small">' + esc(o.notes) + '</span>' : '') + '</td><td class="muted small">' + esc(o.method) + '</td>' +
        '<td>' + pill(o.status) + '</td><td class="muted small">' + esc(o.recorded_by) + '<br/>' + fmtDate(o.recorded_at) + '</td><td>' + act + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No payments recorded yet.</div>';
}
async function addPAY() {
  const bk = resolveId('pay_bk'); if (!bk) return;
  const amt = +val('pay_amt') || 0;
  if (amt < 0) { toast('Amount must be >= 0', 'err'); return; }
  const r = await api('POST', '/api/payments', {booking_id: bk, amount: amt, method: val('pay_method'), status: document.getElementById('pay_status').value, notes: val('pay_notes')});
  if (r.ok) { toast('Payment recorded'); document.getElementById('pay_amt').value = ''; document.getElementById('pay_method').value = ''; document.getElementById('pay_notes').value = ''; loadPAY(); }
}
async function payStatus(id, st) {
  if (st === 'REFUNDED' && !confirm('Mark this payment REFUNDED?')) return;
  const r = await api('PATCH', '/api/payments/' + id, {status: st});
  if (r.ok) { toast('Payment ' + st); loadPAY(); }
}
async function loadINSP() {
  const r = await api('GET', '/api/inspections'); if (!r.ok) return;
  document.getElementById('insp').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>When</th><th>Status</th><th>Notes</th><th></th></tr>' +
    r.data.map(function (o) {
      const act = o.status === 'SCHEDULED' ? '<button class="small" onclick="inspStatus(\'' + o.id + '\',\'COMPLETED\')">Done</button> ' +
        '<button class="small" onclick="inspReschedule(\'' + o.id + '\')">Reschedule</button> ' +
        '<button class="small" onclick="inspStatus(\'' + o.id + '\',\'CANCELLED\')">Cancel</button> ' +
        '<button class="small" onclick="inspStatus(\'' + o.id + '\',\'NO_SHOW\')">No-show</button>' : '';
      return '<tr><td>' + esc(o.phone) + '<br/>' + sid(o.id) + '</td><td>' + fmtDate(o.scheduled_at) + '</td><td>' + pill(o.status) + '</td>' +
        '<td class="muted small">' + esc(o.notes) + '</td><td>' + act + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No inspections — they arrive when a seller books through WhatsApp.</div>';
}
async function inspStatus(id, st) {
  const r = await api('PATCH', '/api/inspections/' + id, {status: st});
  if (r.ok) { toast('Inspection ' + st); loadINSP(); }
}
async function inspReschedule(id) {
  const v = prompt('New date and time (YYYY-MM-DD HH:MM):');
  if (!v) return;
  const d = new Date(v.replace(' ', 'T'));
  if (isNaN(d)) { toast('Could not read that date', 'err'); return; }
  const r = await api('PATCH', '/api/inspections/' + id, {scheduled_at: d.toISOString()});
  if (r.ok) { toast('Rescheduled'); loadINSP(); }
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
  document.getElementById('fin').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Loan</th><th>Tenure</th><th>Employment</th><th>Income</th><th>Status</th><th></th></tr>' +
    r.data.map(function (o) {
      const act = (o.status === 'NEW' || o.status === 'CONTACTED') ? '<button class="small" onclick="finStatus(\'' + o.id + '\',\'APPROVED\')">Approve</button> <button class="small" onclick="finStatus(\'' + o.id + '\',\'REJECTED\')">Reject</button>' : '';
      return '<tr><td>' + esc(o.phone) + '</td><td>' + fmtMoney(o.loan_amount) + '</td><td>' + esc(o.tenure_months) + ' mo</td><td>' + esc(o.employment) + '</td><td>' + fmtMoney(o.income) + '</td><td>' + pill(o.status) + '</td><td>' + act + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No finance enquiries.</div>';
}
async function loadSELL() {
  const r = await api('GET', '/api/sell-requests'); if (!r.ok) return;
  document.getElementById('sell').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Car</th><th>Year</th><th>KM</th><th>Reg</th><th>Photos</th><th>Status</th><th></th></tr>' +
    r.data.map(function (o) {
      let act = '';
      if (o.status === 'VALUATION_PENDING') act = '<button class="small primary" onclick="sellAccept(\'' + o.id + '\')">Accept</button> <button class="small danger" onclick="sellReject(\'' + o.id + '\')">Reject</button>';
      else act = '<button class="small" onclick="sellReopen(\'' + o.id + '\')">Reopen</button>';
      return '<tr><td>' + esc(o.phone) + '<br/>' + sid(o.id) + '</td><td><b>' + esc(o.brand) + ' ' + esc(o.model) + '</b><br/><span class="muted small">' + esc(o.fuel) + ' · ' + esc(o.transmission) + ' · ' + esc(o.condition) + ' · ' + esc(o.location) + '</span></td><td>' + esc(o.year) + '</td><td>' + Number(o.km || 0).toLocaleString('en-IN') + '</td><td class="small">' + esc(o.registration) + '</td><td>' + esc(o.photo_count) + '</td><td>' + pill(o.status) + '</td><td>' + act + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No sell requests.</div>';
}
async function loadRV() {
  const r = await api('GET', '/api/reviews'); if (!r.ok) return;
  document.getElementById('rev').innerHTML = r.data.length ? '<table><tr><th>Customer</th><th>Rating</th><th>Review</th><th></th></tr>' +
    r.data.map(function (o) {
      const del = ME.role === 'admin' ? '<button class="small danger" onclick="delRV(\'' + o.id + '\')">Delete</button>' : '';
      return '<tr><td>' + esc(o.phone) + '</td><td>' + '★'.repeat(+o.rating || 0) + '</td><td>' + esc(o.review) + '</td><td>' + del + '</td></tr>';
    }).join('') + '</table>' : '<div class="empty">No reviews yet.</div>';
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
  document.getElementById('users').innerHTML = '<table><tr><th>Email</th><th>Role</th><th>Since</th><th></th></tr>' +
    USERS.map(function (u) {
      const own = ME.email === u.email;
      const acts = own ? '<span class="muted small">you</span>' :
        '<select class="small" onchange="userRole(\'' + u.id + '\',this.value)"><option value="">Set role…</option><option value="sales">sales</option><option value="admin">admin</option></select> ' +
        '<button class="small danger" onclick="delUser(\'' + u.id + '\',\'' + esc(u.email) + '\')">Remove</button>';
      return '<tr><td>' + esc(u.email) + '</td><td>' + pill(u.role) + '</td><td class="muted small">' + fmtDate(u.created_at) + '</td><td>' + acts + '</td></tr>';
    }).join('') + '</table>';
}
async function addUser() {
  const r = await api('POST', '/api/users', {email: val('u_email'), password: document.getElementById('u_pass').value, role: val('u_role')});
  if (r.ok) { toast('Member added'); document.getElementById('u_email').value = ''; document.getElementById('u_pass').value = ''; loadUsers(); }
}

/* ---------- settings ---------- */
const ZONES = ['Asia/Kolkata', 'Asia/Colombo', 'Asia/Dubai', 'Asia/Muscat', 'Asia/Qatar', 'Asia/Riyadh', 'Asia/Kuwait', 'Asia/Singapore', 'Asia/Kuala_Lumpur', 'Europe/London', 'UTC'];
async function loadSettings() {
  if (ME.role !== 'admin') return;
  const sel = document.getElementById('set_tz');
  sel.innerHTML = ZONES.map(function (z) { return '<option>' + z + '</option>'; }).join('');
  const r = await api('GET', '/api/settings');
  if (r.ok && r.data.timezone) {
    if (!ZONES.includes(r.data.timezone)) { const o = document.createElement('option'); o.textContent = r.data.timezone; sel.appendChild(o); }
    sel.value = r.data.timezone;
    document.getElementById('setOut').textContent = 'Current: ' + r.data.timezone;
  }
}
async function saveSettings() {
  const r = await api('PATCH', '/api/settings', {timezone: document.getElementById('set_tz').value});
  if (r.ok) { toast('Timezone updated — applies to new messages instantly'); loadSettings(); refreshHealth(); }
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

/* ---------- deletes & status actions ---------- */
async function delLead(id) {
  if (!confirm('Delete this lead and its test drives, follow-ups, bookings?')) return;
  const r = await api('DELETE', '/api/leads/' + id);
  if (r.ok) { toast('Lead deleted'); loadLeads(); }
}
async function delVeh(id, label) {
  if (!confirm('Delete vehicle "' + label + '"?')) return;
  const r = await api('DELETE', '/api/vehicles/' + id);
  if (r.ok) { toast('Vehicle deleted'); loadVeh(); }
}
async function tdStatus(id, st) {
  const r = await api('PATCH', '/api/test-drives/' + id, {status: st});
  if (r.ok) { toast('Test drive ' + st); loadTD(); }
}
async function finStatus(id, st) {
  const r = await api('PATCH', '/api/finance/' + id, {status: st});
  if (r.ok) { toast('Finance ' + st); loadFIN(); }
}
async function delFU(id) {
  if (!confirm('Delete this follow-up?')) return;
  const r = await api('DELETE', '/api/followups/' + id);
  if (r.ok) { toast('Deleted'); loadFU(); }
}
async function delRV(id) {
  if (!confirm('Delete this review?')) return;
  const r = await api('DELETE', '/api/reviews/' + id);
  if (r.ok) { toast('Deleted'); loadRV(); }
}
async function userRole(id, role) {
  const r = await api('PATCH', '/api/users/' + id, {role: role});
  if (r.ok) { toast('Role updated'); loadUsers(); }
}
async function delUser(id, email) {
  if (!confirm('Remove team member "' + email + '"?')) return;
  const r = await api('DELETE', '/api/users/' + id);
  if (r.ok) { toast('Removed'); loadUsers(); }
}
async function sellAccept(id) {
  const v = prompt('Accept valuation — enter agreed price in RM:', '0');
  if (v === null) return;
  const r = await api('POST', '/api/sell-requests/' + id + '/accept', {price: +v || 0});
  if (r.ok) { toast('Accepted — car added to inventory'); loadSELL(); loadVeh(); }
}
async function sellReject(id) {
  if (!confirm('Reject this sell request?')) return;
  const r = await api('POST', '/api/sell-requests/' + id + '/reject', {});
  if (r.ok) { toast('Rejected'); loadSELL(); }
}
async function sellReopen(id) {
  const r = await api('POST', '/api/sell-requests/' + id + '/reopen', {});
  if (r.ok) { toast('Reopened'); loadSELL(); }
}

/* ---------- bot configuration (admin) ---------- */
let BOT_FLOWS = [];
let BOT_QUESTIONS = [];
let curBotFlowId = '';
let curBotQuestionId = '';

/* Bot Canvas State */
let dfEditor = null;
let currentBotView = 'canvas';
let dfIdToQId = {};
let qIdToDfId = {};
let isRenderingCanvas = false;
let curCanvasQuestionId = null;
let curCanvasConditions = [];
let questionConditionsMap = {};

function setBotView(mode) {
  currentBotView = mode;
  const btnCanvas = document.getElementById('btn-view-canvas');
  const btnTable = document.getElementById('btn-view-table');
  const viewCanvas = document.getElementById('bot-view-canvas');
  const viewTable = document.getElementById('bot-view-table');

  if (mode === 'canvas') {
    if (btnCanvas) btnCanvas.classList.add('on');
    if (btnTable) btnTable.classList.remove('on');
    if (viewCanvas) viewCanvas.classList.remove('hidden');
    if (viewTable) viewTable.classList.add('hidden');
    initDrawflowIfNeeded();
    loadCanvasFlowsDropdown();
  } else {
    if (btnCanvas) btnCanvas.classList.remove('on');
    if (btnTable) btnTable.classList.add('on');
    if (viewCanvas) viewCanvas.classList.add('hidden');
    if (viewTable) viewTable.classList.remove('hidden');
    closeCanvasDrawer();
    loadBotFlows();
  }
}

function initDrawflowIfNeeded() {
  if (dfEditor) return;
  const container = document.getElementById('drawflow');
  if (!container || typeof Drawflow === 'undefined') return;

  dfEditor = new Drawflow(container);
  dfEditor.reroute = true;
  dfEditor.reroute_fix_curvature = true;
  dfEditor.curvature = 0.5;
  dfEditor.zoom_max = 1.8;
  dfEditor.zoom_min = 0.4;
  dfEditor.zoom_value = 0.1;
  dfEditor.start();

  dfEditor.on('nodeSelected', function(id) {
    onCanvasNodeSelected(id);
  });

  dfEditor.on('connectionCreated', function(info) {
    onCanvasConnectionCreated(info);
  });

  dfEditor.on('connectionRemoved', function(info) {
    onCanvasConnectionRemoved(info);
  });
}

function canvasZoomIn() {
  if (dfEditor) dfEditor.zoom_in();
}
function canvasZoomOut() {
  if (dfEditor) dfEditor.zoom_out();
}
function canvasZoomReset() {
  if (dfEditor) dfEditor.zoom_reset();
}
function autoArrangeCanvas() {
  if (!curBotFlowId) return;
  renderBotCanvas(curBotFlowId, true);
}

async function loadCanvasFlowsDropdown() {
  if (!BOT_FLOWS.length) {
    const r = await api('GET', '/api/bot/flows');
    if (r.ok) BOT_FLOWS = r.data || [];
  }
  const sel = document.getElementById('canvas_flow_select');
  if (!sel) return;
  let opts = BOT_FLOWS.map(function(f) {
    return '<option value="' + esc(f.id) + '">' + esc(f.name) + (f.is_entry_flow ? ' (Entry)' : '') + '</option>';
  }).join('');
  sel.innerHTML = opts || '<option value="">No flows exist</option>';

  if (!curBotFlowId && BOT_FLOWS.length) {
    curBotFlowId = BOT_FLOWS[0].id;
  }
  if (curBotFlowId) {
    sel.value = curBotFlowId;
    renderBotCanvas(curBotFlowId);
  }
}

function onCanvasFlowChange(flowId) {
  curBotFlowId = flowId;
  closeCanvasDrawer();
  const tableSel = document.getElementById('bq_flow_select');
  if (tableSel) tableSel.value = flowId;
  renderBotCanvas(flowId);
}

function loadCanvasCurrentFlow() {
  if (curBotFlowId) renderBotCanvas(curBotFlowId);
}

async function renderBotCanvas(flowId, autoArrange) {
  if (!flowId) return;
  initDrawflowIfNeeded();
  if (!dfEditor) return;

  isRenderingCanvas = true;
  closeCanvasDrawer();
  dfEditor.clear();
  dfIdToQId = {};
  qIdToDfId = {};

  const r = await api('GET', '/api/bot/flows/' + flowId + '/questions');
  if (!r.ok) {
    isRenderingCanvas = false;
    return;
  }
  BOT_QUESTIONS = r.data || [];

  if (!BOT_QUESTIONS.length) {
    isRenderingCanvas = false;
    return;
  }

  // Fetch conditions for all questions in this flow
  const condPromises = BOT_QUESTIONS.map(function(q) {
    return api('GET', '/api/bot/conditions/' + q.id);
  });
  const condResults = await Promise.all(condPromises);
  questionConditionsMap = {};
  BOT_QUESTIONS.forEach(function(q, idx) {
    questionConditionsMap[q.id] = (condResults[idx] && condResults[idx].ok && condResults[idx].data) || [];
  });

  // Calculate layout coordinates and mount nodes
  BOT_QUESTIONS.forEach(function(q, i) {
    const conds = questionConditionsMap[q.id] || [];
    const cols = 4;
    const col = i % cols;
    const row = Math.floor(i / cols);
    const x = 60 + col * 320;
    const y = 80 + row * 240;

    let typePillClass = 'p-gray';
    if (q.question_type === 'number') typePillClass = 'p-blue';
    else if (q.question_type === 'select') typePillClass = 'p-amber';
    else if (q.question_type === 'phone' || q.question_type === 'email') typePillClass = 'p-green';

    const condPills = conds.length ? '<span class="pill p-amber" style="font-size:9px">' + conds.length + ' branch' + (conds.length > 1 ? 'es' : '') + '</span>' : '';

    const nodeHtml = '<div class="df-node-content">' +
      '<div class="df-node-header">' +
        '<span class="df-node-field">#' + q.order_index + ' ' + esc(q.field_name) + '</span>' +
        '<div class="row" style="margin:0;gap:4px">' + condPills + '<span class="pill ' + typePillClass + '" style="font-size:9px">' + esc(q.question_type) + '</span></div>' +
      '</div>' +
      '<div class="df-node-text" title="' + esc(q.question_text) + '">' + esc(q.question_text) + '</div>' +
      '<div class="df-node-ports-label">' +
        '<span>● in</span>' +
        '<span>' + (conds.length ? 'rules ⤳ ' : '') + 'next ●</span>' +
      '</div>' +
    '</div>';

    const outputsCount = 1 + conds.length;
    const dfId = dfEditor.addNode(
      'question',
      1,
      outputsCount,
      x,
      y,
      'question-node',
      { questionId: q.id },
      nodeHtml
    );

    dfIdToQId[dfId] = q.id;
    qIdToDfId[q.id] = dfId;
  });

  // Wire connections between nodes
  BOT_QUESTIONS.forEach(function(q) {
    const srcDfId = qIdToDfId[q.id];
    if (!srcDfId) return;

    // 1. Default sequential wire (output_1 -> input_1)
    if (q.next_question_id && qIdToDfId[q.next_question_id]) {
      const tgtDfId = qIdToDfId[q.next_question_id];
      try {
        dfEditor.addConnection(srcDfId, tgtDfId, 'output_1', 'input_1');
      } catch (e) {
        console.warn('Could not add default connection:', e);
      }
    }

    // 2. Conditional branch wires (output_2, output_3... -> input_1)
    const conds = questionConditionsMap[q.id] || [];
    conds.forEach(function(c, cIdx) {
      if (c.target_question_id && qIdToDfId[c.target_question_id]) {
        const tgtDfId = qIdToDfId[c.target_question_id];
        const outPort = 'output_' + (cIdx + 2);
        try {
          dfEditor.addConnection(srcDfId, tgtDfId, outPort, 'input_1');
        } catch (e) {
          console.warn('Could not add condition connection:', e);
        }
      }
    });
  });

  isRenderingCanvas = false;
}

async function onCanvasConnectionCreated(info) {
  if (isRenderingCanvas) return;
  const srcQId = dfIdToQId[info.output_id];
  const tgtQId = dfIdToQId[info.input_id];
  if (!srcQId || !tgtQId) return;

  if (info.output_class === 'output_1') {
    const r = await api('PATCH', '/api/bot/questions/' + srcQId, { next_question_id: tgtQId });
    if (r.ok) {
      const q = BOT_QUESTIONS.find(function(x) { return x.id === srcQId; });
      if (q) q.next_question_id = tgtQId;
      if (curCanvasQuestionId === srcQId) {
        const nextSel = document.getElementById('cd_next');
        if (nextSel) nextSel.value = tgtQId;
      }
      toast('Connected: Next question updated');
    }
  } else if (info.output_class && info.output_class.startsWith('output_')) {
    const condIndex = parseInt(info.output_class.slice(7), 10) - 2;
    const conds = questionConditionsMap[srcQId] || [];
    if (conds[condIndex]) {
      const cond = conds[condIndex];
      cond.target_question_id = tgtQId;
      const r = await api('PATCH', '/api/bot/conditions/' + cond.id, { target_question_id: tgtQId });
      if (r.ok) {
        toast('Branch rule connected');
        if (curCanvasQuestionId === srcQId) loadCanvasConditions(srcQId);
      }
    }
  }
}

async function onCanvasConnectionRemoved(info) {
  if (isRenderingCanvas) return;
  const srcQId = dfIdToQId[info.output_id];
  if (!srcQId) return;

  if (info.output_class === 'output_1') {
    const r = await api('PATCH', '/api/bot/questions/' + srcQId, { next_question_id: '' });
    if (r.ok) {
      const q = BOT_QUESTIONS.find(function(x) { return x.id === srcQId; });
      if (q) q.next_question_id = null;
      if (curCanvasQuestionId === srcQId) {
        const nextSel = document.getElementById('cd_next');
        if (nextSel) nextSel.value = '';
      }
      toast('Connection removed');
    }
  } else if (info.output_class && info.output_class.startsWith('output_')) {
    const condIndex = parseInt(info.output_class.slice(7), 10) - 2;
    const conds = questionConditionsMap[srcQId] || [];
    if (conds[condIndex]) {
      const cond = conds[condIndex];
      cond.target_question_id = null;
      const r = await api('PATCH', '/api/bot/conditions/' + cond.id, { target_question_id: '' });
      if (r.ok) {
        toast('Branch target cleared');
        if (curCanvasQuestionId === srcQId) loadCanvasConditions(srcQId);
      }
    }
  }
}

function onCanvasNodeSelected(dfNodeId) {
  const qId = dfIdToQId[dfNodeId];
  if (qId) openCanvasDrawer(qId);
}

function openCanvasDrawer(qId) {
  curCanvasQuestionId = qId;
  const q = BOT_QUESTIONS.find(function(x) { return x.id === qId; });
  if (!q) return;

  document.getElementById('cd_id').value = q.id;
  document.getElementById('cd_field').value = q.field_name || '';
  document.getElementById('cd_text').value = q.question_text || '';
  document.getElementById('cd_type').value = q.question_type || 'text';
  document.getElementById('cd_order').value = q.order_index != null ? q.order_index : 0;
  document.getElementById('cd_validation').value = q.validation_rule || '';
  document.getElementById('cd_error').value = q.error_message || '';
  document.getElementById('cd_req').checked = !!q.is_required;

  onCanvasDrawerTypeChange(q.question_type);

  if (q.allowed_values) {
    if (Array.isArray(q.allowed_values)) {
      document.getElementById('cd_allowed').value = q.allowed_values.join('\n');
    } else {
      try {
        const arr = JSON.parse(q.allowed_values);
        document.getElementById('cd_allowed').value = Array.isArray(arr) ? arr.join('\n') : q.allowed_values;
      } catch (e) {
        document.getElementById('cd_allowed').value = q.allowed_values;
      }
    }
  } else {
    document.getElementById('cd_allowed').value = '';
  }

  // Populate next step options
  const nextSel = document.getElementById('cd_next');
  let nextOpts = '<option value="">Sequential (by order #)</option>';
  BOT_QUESTIONS.forEach(function(item) {
    if (item.id !== q.id) {
      nextOpts += '<option value="' + esc(item.id) + '">#' + item.order_index + ' ' + esc(item.field_name) + ' (' + esc(item.question_text.slice(0, 20)) + ')</option>';
    }
  });
  nextSel.innerHTML = nextOpts;
  nextSel.value = q.next_question_id || '';

  // Populate condition target question options
  const condTargetSel = document.getElementById('cd_c_target_q');
  let condTargetOpts = '<option value="">Jump to Question…</option>';
  BOT_QUESTIONS.forEach(function(item) {
    if (item.id !== q.id) {
      condTargetOpts += '<option value="' + esc(item.id) + '">#' + item.order_index + ' ' + esc(item.field_name) + '</option>';
    }
  });
  condTargetSel.innerHTML = condTargetOpts;

  document.getElementById('cd_title').textContent = 'Edit Question (' + esc(q.field_name) + ')';
  loadCanvasConditions(q.id);

  document.getElementById('bot_canvas_drawer').classList.remove('closed');
}

function closeCanvasDrawer() {
  const drawer = document.getElementById('bot_canvas_drawer');
  if (drawer) drawer.classList.add('closed');
  curCanvasQuestionId = null;
}

function onCanvasDrawerTypeChange(type) {
  const wrap = document.getElementById('cd_allowed_wrap');
  if (type === 'select') wrap.classList.remove('hidden');
  else wrap.classList.add('hidden');
}

async function saveCanvasDrawer() {
  if (!curCanvasQuestionId) return;
  const text = val('cd_text');
  const field = val('cd_field');
  const type = val('cd_type');
  const order = parseInt(val('cd_order'), 10) || 0;
  const validation = val('cd_validation');
  const error = val('cd_error');
  const nextId = val('cd_next') || null;
  const req = document.getElementById('cd_req').checked;

  if (!text) { toast('Question text required', 'err'); return; }
  if (!field) { toast('Field name required', 'err'); return; }

  let allowed = [];
  if (type === 'select') {
    allowed = val('cd_allowed').split('\n').map(function(s) { return s.trim(); }).filter(Boolean);
    if (!allowed.length) { toast('Select type requires at least one allowed value', 'err'); return; }
  }

  const payload = {
    question_text: text,
    field_name: field,
    question_type: type,
    order_index: order,
    validation_rule: validation,
    error_message: error,
    next_question_id: nextId,
    is_required: req,
    allowed_values: allowed
  };

  const r = await api('PATCH', '/api/bot/questions/' + curCanvasQuestionId, payload);
  if (r.ok) {
    toast('Question updated');
    const curQ = curCanvasQuestionId;
    await renderBotCanvas(curBotFlowId);
    openCanvasDrawer(curQ);
  }
}

async function quickAddQuestionNode() {
  if (!curBotFlowId) { toast('Select an active flow first', 'err'); return; }
  const nextOrder = (BOT_QUESTIONS.length + 1) * 10;
  const fieldName = 'q_' + (BOT_QUESTIONS.length + 1);
  const payload = {
    flow_id: curBotFlowId,
    field_name: fieldName,
    question_text: 'Please answer the following:',
    question_type: 'text',
    order_index: nextOrder,
    is_required: true,
    allowed_values: []
  };
  const r = await api('POST', '/api/bot/questions', payload);
  if (r.ok) {
    toast('Question added');
    await renderBotCanvas(curBotFlowId);
    if (r.data && r.data.id) openCanvasDrawer(r.data.id);
  }
}

async function deleteCanvasQuestion() {
  if (!curCanvasQuestionId) return;
  if (!confirm('Are you sure you want to delete this question and all its connections?')) return;
  const r = await api('DELETE', '/api/bot/questions/' + curCanvasQuestionId);
  if (r.ok) {
    toast('Question deleted');
    closeCanvasDrawer();
    renderBotCanvas(curBotFlowId);
  }
}

async function loadCanvasConditions(qId) {
  const r = await api('GET', '/api/bot/conditions/' + qId);
  curCanvasConditions = (r.ok && r.data) || [];
  questionConditionsMap[qId] = curCanvasConditions;
  const el = document.getElementById('cd_conditions_list');
  if (!curCanvasConditions.length) {
    el.innerHTML = '<div class="empty" style="padding:8px;font-size:11px">No branching conditions for this question.</div>';
    return;
  }
  let h = '';
  curCanvasConditions.forEach(function(c) {
    let targetName = 'Sequential';
    if (c.target_question_id) {
      const tq = BOT_QUESTIONS.find(function(x) { return x.id === c.target_question_id; });
      targetName = tq ? '#' + tq.order_index + ' ' + tq.field_name : sid(c.target_question_id);
    }
    h += '<div class="row" style="margin:4px 0;background:#f8fafc;padding:6px 8px;border-radius:6px;border:1px solid var(--line);justify-content:space-between;align-items:center;font-size:11px">';
    h += '<div><code>' + esc(c.operator) + '</code> <b>"' + esc(c.value) + '"</b> ➔ ' + targetName + '</div>';
    h += '<button class="small danger" style="padding:2px 6px;margin:0" onclick="deleteCanvasCondition(\'' + esc(c.id) + '\')">✕</button>';
    h += '</div>';
  });
  el.innerHTML = h;
}

async function addCanvasCondition() {
  if (!curCanvasQuestionId) return;
  const op = val('cd_c_op');
  const value = val('cd_c_val');
  const targetQ = val('cd_c_target_q') || null;

  if (!value) { toast('Value to match is required', 'err'); return; }

  const payload = {
    question_id: curCanvasQuestionId,
    operator: op,
    value: value,
    target_question_id: targetQ
  };

  const r = await api('POST', '/api/bot/conditions', payload);
  if (r.ok) {
    toast('Branch rule added');
    document.getElementById('cd_c_val').value = '';
    loadCanvasConditions(curCanvasQuestionId);
    renderBotCanvas(curBotFlowId);
  }
}

async function deleteCanvasCondition(condId) {
  if (!confirm('Delete this condition rule?')) return;
  const r = await api('DELETE', '/api/bot/conditions/' + condId);
  if (r.ok) {
    toast('Condition rule deleted');
    loadCanvasConditions(curCanvasQuestionId);
    renderBotCanvas(curBotFlowId);
  }
}

function switchBotTab(tab) {
  ['flows', 'questions', 'conditions', 'responses'].forEach(function(t) {
    const el = document.getElementById('bot-tab-' + t);
    const btn = document.getElementById('btn-bot-' + t);
    if (el) {
      if (t === tab) el.classList.remove('hidden');
      else el.classList.add('hidden');
    }
    if (btn) {
      if (t === tab) btn.classList.add('primary');
      else btn.classList.remove('primary');
    }
  });
  if (tab === 'flows') loadBotFlows();
  else if (tab === 'questions') { loadBotFlowsDropdown(); if (curBotFlowId) loadBotQuestions(curBotFlowId); }
  else if (tab === 'conditions') { loadBotQuestionsDropdown(); if (curBotQuestionId) loadBotConditions(curBotQuestionId); }
  else if (tab === 'responses') loadBotResponses();
}

async function loadBotFlows() {
  const r = await api('GET', '/api/bot/flows');
  if (!r.ok) return;
  BOT_FLOWS = r.data || [];
  const canvasSel = document.getElementById('canvas_flow_select');
  if (canvasSel && BOT_FLOWS.length) {
    canvasSel.innerHTML = BOT_FLOWS.map(function(f) {
      return '<option value="' + esc(f.id) + '">' + esc(f.name) + (f.is_entry_flow ? ' (Entry)' : '') + '</option>';
    }).join('');
    if (curBotFlowId) canvasSel.value = curBotFlowId;
  }
  const el = document.getElementById('bot_flows_list');
  if (!BOT_FLOWS.length) {
    el.innerHTML = '<div class="empty">No flows configured yet. Create one above.</div>';
    return;
  }
  let h = '<table><thead><tr><th>Name</th><th>Description</th><th>Entry Flow</th><th>Vehicle Match</th><th>Actions</th></tr></thead><tbody>';
  BOT_FLOWS.forEach(function(f) {
    h += '<tr><td><b>' + esc(f.name) + '</b></td><td>' + esc(f.description || '—') + '</td>';
    h += '<td>' + (f.is_entry_flow ? '<span class="pill p-green">YES</span>' : '<span class="pill p-gray">NO</span>') + '</td>';
    h += '<td>' + (f.trigger_matching ? '<span class="pill p-blue">YES</span>' : '<span class="pill p-gray">NO</span>') + '</td>';
    h += '<td><div class="row" style="margin:0">';
    h += '<button class="small" onclick="goToFlowQuestions(\'' + esc(f.id) + '\')">Questions</button>';
    h += '<button class="small" onclick="editBotFlow(\'' + esc(f.id) + '\')">Edit</button>';
    h += '<button class="small danger" onclick="deleteBotFlow(\'' + esc(f.id) + '\')">Delete</button>';
    h += '</div></td></tr>';
  });
  h += '</tbody></table>';
  el.innerHTML = h;
}

async function addBotFlow() {
  const name = val('bf_name');
  const desc = val('bf_desc');
  const isEntry = document.getElementById('bf_entry').checked;
  const isMatch = document.getElementById('bf_match').checked;
  if (!name) { toast('Flow name required', 'err'); return; }
  const r = await api('POST', '/api/bot/flows', {
    name: name,
    description: desc,
    is_entry_flow: isEntry,
    trigger_matching: isMatch
  });
  if (r.ok) {
    toast('Flow created');
    document.getElementById('bf_name').value = '';
    document.getElementById('bf_desc').value = '';
    document.getElementById('bf_entry').checked = false;
    document.getElementById('bf_match').checked = false;
    loadBotFlows();
  }
}

async function editBotFlow(id) {
  const f = BOT_FLOWS.find(function(x) { return x.id === id; });
  if (!f) return;
  const newName = prompt('Flow name:', f.name);
  if (newName === null || !newName.trim()) return;
  const newDesc = prompt('Description:', f.description || '');
  if (newDesc === null) return;
  const isEntry = confirm('Make this the entry flow? (Only one entry flow can exist)');
  const isMatch = confirm('Trigger vehicle matching when completed?');
  const r = await api('PUT', '/api/bot/flows/' + id, {
    name: newName.trim(),
    description: newDesc.trim(),
    is_entry_flow: isEntry,
    trigger_matching: isMatch
  });
  if (r.ok) {
    toast('Flow updated');
    loadBotFlows();
  }
}

async function deleteBotFlow(id) {
  if (!confirm('Are you sure you want to delete this flow and ALL associated questions?')) return;
  const r = await api('DELETE', '/api/bot/flows/' + id);
  if (r.ok) {
    toast('Flow deleted');
    if (curBotFlowId === id) curBotFlowId = '';
    loadBotFlows();
  }
}

function goToFlowQuestions(flowId) {
  curBotFlowId = flowId;
  if (currentBotView === 'canvas') {
    const sel = document.getElementById('canvas_flow_select');
    if (sel) sel.value = flowId;
    renderBotCanvas(flowId);
  } else {
    switchBotTab('questions');
  }
}

async function loadBotFlowsDropdown() {
  if (!BOT_FLOWS.length) {
    const r = await api('GET', '/api/bot/flows');
    if (r.ok) BOT_FLOWS = r.data || [];
  }
  const sel = document.getElementById('bq_flow_select');
  const selTf = document.getElementById('bc_target_f');
  let opts = BOT_FLOWS.map(function(f) {
    return '<option value="' + esc(f.id) + '">' + esc(f.name) + (f.is_entry_flow ? ' (Entry)' : '') + '</option>';
  }).join('');
  sel.innerHTML = opts || '<option value="">No flows exist</option>';
  if (selTf) selTf.innerHTML = '<option value="">Or Jump to Flow…</option>' + opts;
  if (!curBotFlowId && BOT_FLOWS.length) curBotFlowId = BOT_FLOWS[0].id;
  if (curBotFlowId) sel.value = curBotFlowId;
}

function onBotFlowSelected(flowId) {
  curBotFlowId = flowId;
  cancelEditBotQuestion();
  loadBotQuestions(flowId);
}

function onQuestionTypeChange(type) {
  const wrap = document.getElementById('bq_allowed_wrap');
  if (type === 'select') wrap.classList.remove('hidden');
  else wrap.classList.add('hidden');
}

async function loadBotQuestions(flowId) {
  if (!flowId) {
    document.getElementById('bot_questions_list').innerHTML = '<div class="empty">Please select a flow first.</div>';
    return;
  }
  const r = await api('GET', '/api/bot/flows/' + flowId + '/questions');
  if (!r.ok) return;
  BOT_QUESTIONS = r.data || [];
  
  // Populate next question dropdown
  const nextSel = document.getElementById('bq_next');
  let nextOpts = '<option value="">Default Next Question (by order)…</option>';
  BOT_QUESTIONS.forEach(function(q) {
    nextOpts += '<option value="' + esc(q.id) + '">#' + q.order_index + ' ' + esc(q.field_name) + ' (' + esc(q.question_text.slice(0, 24)) + ')</option>';
  });
  nextSel.innerHTML = nextOpts;

  const el = document.getElementById('bot_questions_list');
  if (!BOT_QUESTIONS.length) {
    el.innerHTML = '<div class="empty">No questions configured for this flow yet. Add one above.</div>';
    return;
  }
  let h = '<table><thead><tr><th>#</th><th>Field</th><th>Question Text</th><th>Type</th><th>Validation</th><th>Next</th><th>Actions</th></tr></thead><tbody>';
  BOT_QUESTIONS.forEach(function(q) {
    let nextLabel = 'Next (#)';
    if (q.next_question_id) {
      const nq = BOT_QUESTIONS.find(function(x) { return x.id === q.next_question_id; });
      nextLabel = nq ? '#' + nq.order_index + ' ' + nq.field_name : sid(q.next_question_id);
    }
    h += '<tr><td><b>' + q.order_index + '</b></td>';
    h += '<td><code>' + esc(q.field_name) + '</code>' + (q.is_required ? '' : ' <span class="muted small">(opt)</span>') + '</td>';
    h += '<td>' + esc(q.question_text) + '</td>';
    h += '<td><span class="pill p-gray">' + esc(q.question_type) + '</span></td>';
    h += '<td><span class="small muted">' + esc(q.validation_rule || 'none') + '</span></td>';
    h += '<td><span class="small">' + nextLabel + '</span></td>';
    h += '<td><div class="row" style="margin:0">';
    h += '<button class="small" onclick="goToQuestionConditions(\'' + esc(q.id) + '\')">Conditions</button>';
    h += '<button class="small" onclick="editBotQuestion(\'' + esc(q.id) + '\')">Edit</button>';
    h += '<button class="small danger" onclick="deleteBotQuestion(\'' + esc(q.id) + '\')">Delete</button>';
    h += '</div></td></tr>';
  });
  h += '</tbody></table>';
  el.innerHTML = h;
}

async function saveBotQuestion() {
  if (!curBotFlowId) { toast('Please select a flow first', 'err'); return; }
  const editId = val('bq_edit_id');
  const text = val('bq_text');
  const field = val('bq_field');
  const type = val('bq_type');
  const order = parseInt(val('bq_order'), 10) || 0;
  const validation = val('bq_validation');
  const error = val('bq_error');
  const nextId = val('bq_next') || null;
  const req = document.getElementById('bq_req').checked;

  if (!text) { toast('Question text required', 'err'); return; }
  if (!field) { toast('Field name required', 'err'); return; }

  let allowed = null;
  if (type === 'select') {
    const lines = val('bq_allowed').split('\n').map(function(s) { return s.trim(); }).filter(Boolean);
    if (!lines.length) { toast('Select type requires at least one allowed value', 'err'); return; }
    allowed = JSON.stringify(lines);
  }

  const payload = {
    question_text: text,
    field_name: field,
    question_type: type,
    validation_rule: validation,
    allowed_values: allowed,
    error_message: error,
    next_question_id: nextId,
    is_required: req,
    order_index: order
  };

  let r;
  if (editId) {
    r = await api('PUT', '/api/bot/questions/' + editId, payload);
  } else {
    r = await api('POST', '/api/bot/flows/' + curBotFlowId + '/questions', payload);
  }

  if (r.ok) {
    toast(editId ? 'Question updated' : 'Question added');
    cancelEditBotQuestion();
    loadBotQuestions(curBotFlowId);
  }
}

function editBotQuestion(id) {
  const q = BOT_QUESTIONS.find(function(x) { return x.id === id; });
  if (!q) return;
  document.getElementById('bq_edit_id').value = q.id;
  document.getElementById('bq_text').value = q.question_text || '';
  document.getElementById('bq_field').value = q.field_name || '';
  document.getElementById('bq_type').value = q.question_type || 'text';
  document.getElementById('bq_order').value = q.order_index != null ? q.order_index : 0;
  document.getElementById('bq_validation').value = q.validation_rule || '';
  document.getElementById('bq_error').value = q.error_message || '';
  document.getElementById('bq_next').value = q.next_question_id || '';
  document.getElementById('bq_req').checked = !!q.is_required;

  onQuestionTypeChange(q.question_type);
  if (q.allowed_values) {
    try {
      const arr = JSON.parse(q.allowed_values);
      document.getElementById('bq_allowed').value = Array.isArray(arr) ? arr.join('\n') : q.allowed_values;
    } catch (e) {
      document.getElementById('bq_allowed').value = q.allowed_values;
    }
  } else {
    document.getElementById('bq_allowed').value = '';
  }

  document.getElementById('bq_form_title').textContent = 'Edit Question (' + q.field_name + ')';
  document.getElementById('bq_submit_btn').textContent = 'Update Question';
  document.getElementById('bq_cancel_btn').classList.remove('hidden');
  document.getElementById('bq_form_panel').scrollIntoView({behavior: 'smooth'});
}

function cancelEditBotQuestion() {
  document.getElementById('bq_edit_id').value = '';
  document.getElementById('bq_text').value = '';
  document.getElementById('bq_field').value = '';
  document.getElementById('bq_type').value = 'text';
  document.getElementById('bq_order').value = (BOT_QUESTIONS.length + 1) * 10;
  document.getElementById('bq_validation').value = '';
  document.getElementById('bq_error').value = '';
  document.getElementById('bq_next').value = '';
  document.getElementById('bq_req').checked = true;
  document.getElementById('bq_allowed').value = '';
  onQuestionTypeChange('text');

  document.getElementById('bq_form_title').textContent = 'Add Question';
  document.getElementById('bq_submit_btn').textContent = 'Add Question';
  document.getElementById('bq_cancel_btn').classList.add('hidden');
}

async function deleteBotQuestion(id) {
  if (!confirm('Are you sure you want to delete this question?')) return;
  const r = await api('DELETE', '/api/bot/questions/' + id);
  if (r.ok) {
    toast('Question deleted');
    loadBotQuestions(curBotFlowId);
  }
}

function goToQuestionConditions(questionId) {
  curBotQuestionId = questionId;
  switchBotTab('conditions');
}

async function loadBotQuestionsDropdown() {
  const selQ = document.getElementById('bc_question_select');
  const selTq = document.getElementById('bc_target_q');
  if (!curBotFlowId && BOT_FLOWS.length) curBotFlowId = BOT_FLOWS[0].id;
  if (curBotFlowId) {
    const r = await api('GET', '/api/bot/flows/' + curBotFlowId + '/questions');
    if (r.ok) {
      const qs = r.data || [];
      let qOpts = qs.map(function(q) {
        return '<option value="' + esc(q.id) + '">#' + q.order_index + ' ' + esc(q.field_name) + ' — ' + esc(q.question_text.slice(0, 30)) + '</option>';
      }).join('');
      selQ.innerHTML = qOpts || '<option value="">No questions in current flow</option>';
      selTq.innerHTML = '<option value="">Jump to Question…</option>' + qOpts;
      if (!curBotQuestionId && qs.length) curBotQuestionId = qs[0].id;
      if (curBotQuestionId) selQ.value = curBotQuestionId;
    }
  }
  loadBotFlowsDropdown();
}

async function loadBotConditions(questionId) {
  if (!questionId) {
    document.getElementById('bot_conditions_list').innerHTML = '<div class="empty">Please select a question above.</div>';
    return;
  }
  curBotQuestionId = questionId;
  const r = await api('GET', '/api/bot/questions/' + questionId + '/conditions');
  if (!r.ok) return;
  const conds = r.data || [];
  const el = document.getElementById('bot_conditions_list');
  if (!conds.length) {
    el.innerHTML = '<div class="empty">No branching conditions for this question. Default routing applies.</div>';
    return;
  }
  let h = '<table><thead><tr><th>Operator</th><th>Value</th><th>Target</th><th>Actions</th></tr></thead><tbody>';
  conds.forEach(function(c) {
    let targetStr = '—';
    if (c.target_question_id) targetStr = 'Question: ' + sid(c.target_question_id);
    else if (c.target_flow_id) targetStr = 'Flow: ' + sid(c.target_flow_id);
    h += '<tr><td><code>' + esc(c.condition_operator) + '</code></td>';
    h += '<td><b>' + esc(c.condition_value) + '</b></td>';
    h += '<td>' + targetStr + '</td>';
    h += '<td><button class="small danger" onclick="deleteBotCondition(\'' + esc(c.id) + '\')">Delete</button></td></tr>';
  });
  h += '</tbody></table>';
  el.innerHTML = h;
}

async function addBotCondition() {
  const qId = val('bc_question_select') || curBotQuestionId;
  if (!qId) { toast('Please select a question', 'err'); return; }
  const op = val('bc_op');
  const value = val('bc_val');
  const targetQ = val('bc_target_q') || null;
  const targetF = val('bc_target_f') || null;

  if (!value) { toast('Value required', 'err'); return; }
  if (!targetQ && !targetF) { toast('Specify a target question or target flow', 'err'); return; }

  const r = await api('POST', '/api/bot/questions/' + qId + '/conditions', {
    condition_operator: op,
    condition_value: value,
    target_question_id: targetQ,
    target_flow_id: targetF
  });
  if (r.ok) {
    toast('Condition added');
    document.getElementById('bc_val').value = '';
    loadBotConditions(qId);
  }
}

async function deleteBotCondition(id) {
  if (!confirm('Delete this condition?')) return;
  const r = await api('DELETE', '/api/bot/conditions/' + id);
  if (r.ok) {
    toast('Condition deleted');
    loadBotConditions(val('bc_question_select') || curBotQuestionId);
  }
}

async function loadBotResponses() {
  const r = await api('GET', '/api/bot/responses');
  if (!r.ok) return;
  const resps = r.data || [];
  const el = document.getElementById('bot_responses_list');
  if (!resps.length) {
    el.innerHTML = '<div class="empty">No system responses configured.</div>';
    return;
  }
  let h = '<div style="display:flex;flex-direction:column;gap:12px">';
  resps.forEach(function(item) {
    const tid = 'br_txt_' + item.id.replace(/-/g, '_');
    h += '<div class="panel" style="margin:0"><h4><code>' + esc(item.response_key) + '</code> <span class="muted small">' + esc(item.description || '') + '</span></h4>';
    h += '<textarea id="' + tid + '" style="width:100%;height:70px;margin-bottom:8px">' + esc(item.message_text) + '</textarea>';
    h += '<div class="row" style="margin:0"><button class="small primary" onclick="saveBotResponse(\'' + esc(item.id) + '\', \'' + tid + '\')">Save Template</button></div>';
    h += '</div>';
  });
  h += '</div>';
  el.innerHTML = h;
}

async function saveBotResponse(id, tid) {
  const text = document.getElementById(tid).value;
  const r = await api('PUT', '/api/bot/responses/' + id, {message_text: text});
  if (r.ok) {
    toast('Response template saved');
  }
}

