import json, os, urllib.request, time

BASE = os.environ.get("BASE_URL", "http://127.0.0.1:18082")
RUN = str(int(time.time()) % 100000)
def ph(n): return "9" + RUN.rjust(5, "0")[:5] + n
TOKEN = open(os.environ.get("TOKEN_FILE", "/tmp/sb-token")).read().strip()
fails = []
def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name, extra)
    if not cond: fails.append(name)

def api(method, path, body=None, token=TOKEN, raw=None):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(BASE + path, method=method, data=data,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        try: return e.code, json.loads(e.read() or b"null")
        except Exception: return e.code, None

def sim(phone, body):
    s, j = api("POST", "/api/whatsapp/simulate", {"phone": phone, "body": body})
    return (j or {}).get("reply", "")

# STATE-005 back
sim(ph("1"), "hi"); sim(ph("1"), "buy"); sim(ph("1"), "4 lakh")
r = sim(ph("1"), "back")
check("STATE-005 back", "back one step" in r.lower() and "budget" in r.lower(), r[:90])

# STATE-006 start again
sim(ph("1"), "maruti")
r = sim(ph("1"), "start again")
check("STATE-006 restart", "starting fresh" in r.lower() and "buy" in r.lower(), r[:90])

# P0-13 intent switch mid-flow
sim(ph("2"), "hi"); sim(ph("2"), "buy"); sim(ph("2"), "5 lakh")
r = sim(ph("2"), "actually i want to sell my car")
check("P0-13 intent switch", "sell" in r.lower() and "brand" in r.lower(), r[:90])

# Takeover: silence + store
sim(ph("3"), "hi")
s, convs = api("GET", "/api/conversations?limit=50")
cid = [c for c in convs if c["phone"] == ph("3")][0]["id"]
s, msgs0 = api("GET", f"/api/messages?conversation_id={cid}")
api("PATCH", f"/api/conversations?id={cid}", {"bot_enabled": False})
r = sim(ph("3"), "hello anyone there")
s, msgs1 = api("GET", f"/api/messages?conversation_id={cid}")
check("STATE-007 takeover silent+stored", r == "" and len(msgs1) == len(msgs0) + 1, f"reply={r!r} {len(msgs0)}->{len(msgs1)}")
api("PATCH", f"/api/conversations?id={cid}", {"bot_enabled": True})

# Negative / zero budget reprompt
sim(ph("4"), "hi"); sim(ph("4"), "buy")
r = sim(ph("4"), "-5 lakh")
check("BUY-106 negative reprompt", "didn't catch" in r.lower(), r[:70])

# Tanglish + emoji + Indian-format budget
r = sim(ph("5"), "bro BMW venum 😊")
check("tolerant+Tanglish+emoji", "budget" in r.lower(), r[:70])
r = sim(ph("5"), "₹15,00,000")
check("BUY-104 indian format", "model" in r.lower(), r[:70])

# SELL invalid details reprompt
sim(ph("6"), "hi"); sim(ph("6"), "sell"); sim(ph("6"), "Swift")
r = sim(ph("6"), "nothing here really")
check("SELL details validation", "year" in r.lower() and "km" in r.lower(), r[:80])

# INT-003/004 rapid + out-of-order burst -> exactly one lead, sane end state
for m in ["hi", "buy", "BMW", "X1", "Diesel", "Automatic", "20 lakh", "2020"]:
    r = sim(ph("7"), m)
s, leads = api("GET", "/api/leads")
mine = [l for l in leads if l["phone"] == ph("7")]
check("INT-003 one lead sane state", len(mine) == 1 and mine[0]["state"] == "BUY_RESULTS", mine)

# Transition guard
s, v = api("POST", "/api/vehicles", {"make": "Tata", "model": "Nexon", "year": 2022, "price": 900000})
vid = v["id"]
s, _ = api("PATCH", f"/api/vehicles/{vid}", {"status": "DELIVERED"})
check("transition guard 400", s == 400, s)
s, _ = api("PATCH", f"/api/vehicles/{vid}?force=1", {"status": "DELIVERED"})
check("transition force ok", s == 200, s)

# Upload: exe rejected, png ok + dedup
s, _ = api("POST", f"/api/vehicles/{vid}/images", raw=b"fake-exe-bytes", token=TOKEN) if False else (None, None)
import urllib.request as u2
def upload(data, fname="x.png"):
    import uuid as U
    b = f'------B\r\nContent-Disposition: form-data; name="file"; filename="{fname}"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode() + data + b'\r\n------B--\r\n'
    req = u2.Request(BASE + f"/api/vehicles/{vid}/images", method="POST", data=b,
        headers={"Content-Type": "multipart/form-data; boundary=----B", "Authorization": f"Bearer {TOKEN}"})
    try:
        with u2.urlopen(req) as r: return r.status, json.loads(r.read())
    except u2.HTTPError as e:
        return e.code, e.read()[:80]
png = bytes.fromhex("89504e470d0a1a0a") + b"\x00" * 100
s, j = upload(b"MZ" + b"\x00" * 100, "evil.exe")
check("SEC file rejected", s == 400, (s, j))
s, j1 = upload(png, "front.png")
s, j2 = upload(png, "front-copy.png")
check("upload ok + content dedup", s == 200 and j2.get("duplicate") is True, (j1, j2))

# Metrics + health disk
s, m = api("GET", "/api/metrics")
check("metrics", s == 200 and m.get("goroutines", 0) > 0, m)
import json as J
h = J.loads(urllib.request.urlopen(BASE + "/api/health").read())
check("health disk pct", h.get("disk_used_pct", -1) >= 0, h)

# Audit trail via assign
s, leads = api("GET", "/api/leads")
lid = [l for l in leads if l["phone"] == ph("7")][0]["id"]
api("PATCH", f"/api/leads/{lid}/assign", {"sales_user_id": None})
print("ASSIGN_DONE", lid)

# Regression: match/book/deliver/review/RBAC on the INT-003 lead (budget 16-20L)
s, v = api("POST", "/api/vehicles", {"make": "Mahindra", "model": "XUV700", "year": 2022, "price": 1800000, "fuel": "DIESEL", "transmission": "MANUAL", "km": 30000})
vid = v["id"]
s, m = api("POST", f"/api/leads/{lid}/match")
check("MATCH exact fires", any(x["vehicle_id"] == vid for x in m), m)
s, td = api("POST", "/api/test-drives", {"lead_id": lid, "vehicle_id": vid, "scheduled_at": "2026-11-01T10:00:00Z"})
check("TD book", s == 200, (s, td))
s2, _ = api("POST", "/api/test-drives", {"lead_id": lid, "vehicle_id": vid, "scheduled_at": "2026-11-01T10:00:00Z"})
check("TD double 409", s2 == 409, s2)
s, _ = api("POST", "/api/negotiations", {"lead_id": lid, "vehicle_id": vid, "customer_offer": 1750000, "final_price": 1780000})
check("negotiation", s == 200, s)
s, _ = api("PATCH", f"/api/leads/{lid}/interest", {"interest": "INTERESTED"})
check("interest", s == 200, s)
s, u = api("POST", "/api/users", {"email": "sales2@local.test", "password": "sales123", "role": "sales"})
stok = api("POST", "/api/auth/login", {"email": "sales2@local.test", "password": "sales123"}, token="x")[1]["token"]
s, _ = api("PATCH", f"/api/leads/{lid}/assign", {"sales_user_id": u["id"]}, token=stok)
check("sales assign 403", s == 403, s)
api("PATCH", f"/api/leads/{lid}/assign", {"sales_user_id": u["id"]})
s, sl = api("GET", "/api/leads", token=stok)
check("sales scoped view", any(l["id"] == lid for l in sl), len(sl))
s, bk = api("POST", "/api/bookings", {"lead_id": lid, "vehicle_id": vid, "amount": 1780000})
check("booking", s == 200, (s, bk))
s2, _ = api("POST", "/api/bookings", {"lead_id": lid, "vehicle_id": vid, "amount": 1780000})
check("double-book 409", s2 == 409, s2)
api("PATCH", f"/api/bookings/{bk['id']}", {"status": "COMPLETED"})
s, rv = api("POST", "/api/reviews", {"booking_id": bk["id"], "rating": 5, "review": "Great"})
check("review", s == 200, (s, rv))
s, dash = api("GET", "/api/dashboard")
check("dashboard", s == 200 and dash["leads"] >= 7, dash)

print(f"\n{len(fails)} failed: {fails}" if fails else "\nALL DEEP CASES PASS")
raise SystemExit(1 if fails else 0)
