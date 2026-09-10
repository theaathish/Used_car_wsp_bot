let T = localStorage.getItem('token') || '';
if (T) { document.getElementById('login').classList.add('hidden'); document.getElementById('app').classList.remove('hidden'); boot(); }
function H(extra={}){ return Object.assign({'Content-Type':'application/json','Authorization':'Bearer '+T}, extra); }
async function login(){
  const email=document.getElementById('email').value, password=document.getElementById('password').value;
  const r=await fetch('/api/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email,password})});
  const j=await r.json();
  if(!r.ok){ document.getElementById('loginErr').textContent=JSON.stringify(j); return; }
  T=j.token; localStorage.setItem('token',T);
  document.getElementById('login').classList.add('hidden'); document.getElementById('app').classList.remove('hidden'); boot();
}
function logout(){ localStorage.removeItem('token'); location.reload(); }
function show(k){ document.querySelectorAll('section').forEach(s=>s.classList.remove('active')); document.getElementById('s-'+k).classList.add('active'); }
async function boot(){ health(); dash(); waStatus(); loadLeads(); loadConv(); loadVeh(); loadTD(); loadFU(); loadBK(); loadNG(); loadFIN(); loadSELL(); loadRV(); }
async function health(){ const r=await fetch('/api/health'); const j=await r.json(); document.getElementById('health').textContent='db:'+j.db+' wa:'+(j.whatsapp&&j.whatsapp.status); }
async function dash(){ const r=await fetch('/api/dashboard',{headers:H()}); const j=await r.json();
  document.getElementById('cards').innerHTML=['leads|Leads','available_vehicles|Vehicles','scheduled_test_drives|Test drives','pending_followups|Followups','open_bookings|Bookings'].map(s=>{const[k,l]=s.split('|');return `<div class=card><div>${l}</div><b>${j[k]??0}</b></div>`}).join(''); }
function esc(s){ return String(s??'').replace(/[&<>"']/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function tbl(rows, cols){ if(!rows.length) return '<i>empty</i>';
  return '<table><tr>'+cols.map(c=>`<th>${esc(c)}</th>`).join('')+'</tr>'+rows.map(o=>'<tr>'+cols.map(c=>`<td>${esc((o[c]??'').toString().slice(0,120))}</td>`).join('')+'</tr>').join('')+'</table>'; }
async function waStatus(){ const r=await fetch('/api/whatsapp/status',{headers:H()}); const j=await r.json();
  document.getElementById('waOut').textContent=JSON.stringify(j,null,2);
  const w=document.getElementById('qrWrap');
  if(j.has_qr){ w.innerHTML='<p>Scan QR with WhatsApp > Linked devices:</p><img class=qr src="/api/whatsapp/qr" />'; }
  else w.innerHTML=''; }
async function waLogout(){ await fetch('/api/whatsapp/logout',{method:'POST',headers:H()}); waStatus(); }
async function loadLeads(){ const r=await fetch('/api/leads?limit=50',{headers:H()}); const j=await r.json(); document.getElementById('leads').innerHTML=tbl(j,['id','customer','phone','intent','status','state']); }
async function loadConv(){ const r=await fetch('/api/conversations?limit=30',{headers:H()}); const j=await r.json(); document.getElementById('conv').innerHTML=tbl(j,['id','name','phone','status','messages']); }
async function loadMsg(){ const id=document.getElementById('convId').value; const r=await fetch('/api/messages?conversation_id='+id,{headers:H()}); document.getElementById('msg').textContent=JSON.stringify(await r.json(),null,2); }
async function loadVeh(){ const r=await fetch('/api/vehicles?limit=50',{headers:H()}); const j=await r.json(); document.getElementById('veh').innerHTML=tbl(j,['id','make','model','year','price','fuel','transmission','status']); }
async function addVehicle(){ const g=id=>document.getElementById(id).value;
  const body={make:g('v_make'),model:g('v_model'),year:+g('v_year'),price:+g('v_price'),fuel:g('v_fuel'),transmission:g('v_trans'),km:+g('v_km')};
  await fetch('/api/vehicles',{method:'POST',headers:H(),body:JSON.stringify(body)}); loadVeh(); }
async function loadTD(){ const r=await fetch('/api/test-drives',{headers:H()}); document.getElementById('td').innerHTML=tbl(await r.json(),['id','phone','vehicle','scheduled_at','status']); }
async function addTD(){ const g=id=>document.getElementById(id).value;
  await fetch('/api/test-drives',{method:'POST',headers:H(),body:JSON.stringify({lead_id:g('td_lead'),vehicle_id:g('td_veh'),scheduled_at:g('td_when')})}); loadTD(); }
async function loadFU(){ const r=await fetch('/api/followups',{headers:H()}); document.getElementById('fu').innerHTML=tbl(await r.json(),['id','phone','type','scheduled_at','status','message']); }
async function addFU(){ const msg=document.getElementById('fu_msg').value, when=document.getElementById('fu_when').value;
  await fetch('/api/followups',{method:'POST',headers:H(),body:JSON.stringify({scheduled_at:when,message:msg})}); loadFU(); }
async function loadBK(){ const r=await fetch('/api/bookings',{headers:H()}); document.getElementById('bk').innerHTML=tbl(await r.json(),['id','phone','vehicle','amount','status']); }
async function addBK(){ const g=id=>document.getElementById(id).value;
  await fetch('/api/bookings',{method:'POST',headers:H(),body:JSON.stringify({lead_id:g('bk_lead'),vehicle_id:g('bk_veh'),amount:+g('bk_amt')})}); loadBK(); }
async function sim(){ const phone=document.getElementById('sim_phone').value, body=document.getElementById('sim_body').value;
  const r=await fetch('/api/whatsapp/simulate',{method:'POST',headers:H(),body:JSON.stringify({phone,body})});
  document.getElementById('simOut').textContent=JSON.stringify(await r.json(),null,2); }
async function loadNG(){ const r=await fetch('/api/negotiations',{headers:H()}); document.getElementById('neg').innerHTML=tbl(await r.json(),['id','phone','vehicle','customer_offer','sales_offer','final_price']); }
async function addNG(){ const g=id=>document.getElementById(id).value;
  await fetch('/api/negotiations',{method:'POST',headers:H(),body:JSON.stringify({lead_id:g('ng_lead'),vehicle_id:g('ng_veh'),customer_offer:+g('ng_co'),sales_offer:+g('ng_so'),final_price:+g('ng_fp')})}); loadNG(); }
async function loadFIN(){ const r=await fetch('/api/finance',{headers:H()}); document.getElementById('fin').innerHTML=tbl(await r.json(),['id','phone','loan_amount','tenure_months','employment','income','status']); }
async function loadSELL(){ const r=await fetch('/api/sell-requests',{headers:H()}); document.getElementById('sell').innerHTML=tbl(await r.json(),['id','phone','brand','model','year','km','status']); }
async function loadRV(){ const r=await fetch('/api/reviews',{headers:H()}); document.getElementById('rev').innerHTML=tbl(await r.json(),['id','phone','rating','review']); }
async function addRV(){ const g=id=>document.getElementById(id).value;
  await fetch('/api/reviews',{method:'POST',headers:H(),body:JSON.stringify({booking_id:g('rv_bk'),rating:+g('rv_rate'),review:g('rv_txt')})}); loadRV(); }
async function addUser(){ const g=id=>document.getElementById(id).value;
  const r=await fetch('/api/users',{method:'POST',headers:H(),body:JSON.stringify({email:g('u_email'),password:g('u_pass'),role:g('u_role')})});
  document.getElementById('teamOut').textContent=JSON.stringify(await r.json(),null,2); }
async function assign(){ const g=id=>document.getElementById(id).value;
  const id=g('as_lead');
  const r=await fetch('/api/leads/'+id+'/assign',{method:'PATCH',headers:H(),body:JSON.stringify({sales_user_id:g('as_user')})});
  document.getElementById('teamOut').textContent=JSON.stringify(await r.json(),null,2); }
