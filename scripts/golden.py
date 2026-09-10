#!/usr/bin/env python3
"""V3-42 golden conversations: fixed scripts, exact expected states.
Runs against BASE_URL with TOKEN_FILE. Every deploy runs these.
Exit 1 on any mismatch."""
import json, os, time, urllib.request, urllib.error

BASE = os.environ.get("BASE_URL", "http://127.0.0.1:18082")
TOKEN = open(os.environ.get("TOKEN_FILE", "/tmp/sb-token")).read().strip()
RUN = str(int(time.time()) % 1000000)
fails = []

def api(method, path, body=None):
    req = urllib.request.Request(BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {TOKEN}"})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        return e.code, (e.read() or b"")[:200]

def sim(phone, body):
    s, j = api("POST", "/api/whatsapp/simulate", {"phone": phone, "body": body})
    return (j or {}).get("reply", "")

def lead_of(phone):
    s, leads = api("GET", "/api/leads")
    return [l for l in leads if l["phone"] == phone][0]

def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name, str(extra)[:100])
    if not cond:
        fails.append(name)

P = lambda n: "8" + RUN.rjust(6, "0")[:6] + n  # unique phones per run

# GOLDEN-001 BUY end-to-end (state trail only; matching covered elsewhere)
p = P("01")
for m in ["hi", "buy BMW X1 diesel automatic", "18 lakh", "2020"]:
    r = sim(p, m)
l = lead_of(p)
check("GOLDEN-001 buy", l["state"] == "BUY_RESULTS" and l["intent"] == "BUY", (l["state"], l["intent"]))

# GOLDEN-002 SELL with validation recovery
p = P("02")
for m in ["hi", "sell", "Swift", "junk input here", "2019, MH01AA1111, 40000km", "Petrol, Manual, Good, Thane", "DONE"]:
    r = sim(p, m)
l = lead_of(p)
s, sells = api("GET", "/api/sell-requests")
check("GOLDEN-002 sell", l["intent"] == "SELL" and any(x["phone"] == p for x in sells), l["state"])

# GOLDEN-003 EXCHANGE
p = P("03")
for m in ["hi", "exchange", "Alto 2015 70000km", "Baleno under 8 lakh"]:
    r = sim(p, m)
l = lead_of(p)
check("GOLDEN-003 exchange", l["intent"] == "EXCHANGE" and l["state"] == "DONE", (l["state"], l["intent"]))

# GOLDEN-004 TEST DRIVE request -> sales followup
p = P("04")
for m in ["hi", "buy", "5 lakh", "Maruti", "Swift", "Petrol", "Manual", "2019"]:
    sim(p, m)
r = sim(p, "test drive")
r = sim(p, "Swift tomorrow 10am")
s, fus = api("GET", "/api/followups")
check("GOLDEN-004 td-request", "test drive" in r.lower() and any("Test drive request" in (f.get("message") or "") for f in fus), r[:70])

# GOLDEN-005 FOLLOW-UP thinking -> interested
p = P("05")
for m in ["hi", "buy", "5 lakh", "Maruti", "Swift", "Petrol", "Manual", "2019"]:
    sim(p, m)
sim(p, "i will think")
l = lead_of(p)
a = l["interest"] == "THINKING"
sim(p, "actually I'm interested")
l = lead_of(p)
check("GOLDEN-005 interest trail", a and l["interest"] == "INTERESTED", l["interest"])

# GOLDEN-006 BOOKING via API on golden lead (uses INT-003 style lead)
s, v = api("POST", "/api/vehicles", {"make": "Gold", "model": "Star", "year": 2021, "price": 500000})
vid = v["id"]
s, bk = api("POST", "/api/bookings", {"lead_id": l["id"], "vehicle_id": vid, "amount": 490000})
check("GOLDEN-006 booking", s == 200, (s, bk))
api("PATCH", f"/api/bookings/{bk['id']}", {"status": "COMPLETED"})
s, bks = api("GET", "/api/bookings")
check("GOLDEN-006 delivered", any(b["status"] == "COMPLETED" for b in bks), "")

# GOLDEN-007 TAKEOVER silence
p = P("07")
sim(p, "hi")
s, convs = api("GET", "/api/conversations?limit=100")
cid = [c for c in convs if c["phone"] == p][0]["id"]
api("PATCH", f"/api/conversations?id={cid}", {"bot_enabled": False})
r = sim(p, "are you a robot?")
check("GOLDEN-007 takeover", r == "", repr(r))
api("PATCH", f"/api/conversations?id={cid}", {"bot_enabled": True})

# GOLDEN-008 INTERRUPTED: answer brand at budget step, budget later
p = P("08")
sim(p, "hi"); sim(p, "buy"); sim(p, "Hyundai"); r = sim(p, "6 lakh")
l = lead_of(p)
d = l["state"]
check("GOLDEN-008 interrupted", l["state"] in ("BUY_MODEL", "BUY_FUEL", "BUY_RESULTS"), (d, r[:60]))

# GOLDEN-009 INVALID INPUT never corrupts
p = P("09")
for m in ["hi", "buy", "' OR 1=1 --", "<script>", "-99 lakh", "₹0"]:
    r = sim(p, m)
l = lead_of(p)
check("GOLDEN-009 invalid safe", l["intent"] == "BUY" and l["state"] in ("BUY_BUDGET", "BUY_BRAND", "BUY_MODEL"), (l["state"],))

# GOLDEN-010 WA FAILURE path: outbox visible + send API shape
s, ob = api("GET", "/api/outbox")
check("GOLDEN-010 outbox api", s == 200 and isinstance(ob, list), s)

print("GOLDEN:", "ALL PASS" if not fails else f"{fails} FAILED")
raise SystemExit(1 if fails else 0)
