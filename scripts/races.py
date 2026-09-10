#!/usr/bin/env python3
"""V3 races: bookingx100, TDx100, dedupx100, bursts, book-vs-sold.
Exit 1 on any invariant break."""
import json, os, time, urllib.request, urllib.error
from concurrent.futures import ThreadPoolExecutor

BASE = os.environ.get("BASE_URL", "http://127.0.0.1:18082")
TOKEN = open(os.environ.get("TOKEN_FILE", "/tmp/sb-token")).read().strip()
RUN = str(int(time.time()) % 1000000)
fails = []

def api(method, path, body=None, token=TOKEN):
    req = urllib.request.Request(BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        return e.code, None
    except Exception as e:
        return -1, repr(e)

def sim(phone, body, waid=None):
    b = {"phone": phone, "body": body}
    s, j = api("POST", "/api/whatsapp/simulate", b)
    return (j or {}).get("reply", "")

def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name, str(extra)[:120])
    if not cond:
        fails.append(name)

P = lambda n: "7" + RUN.rjust(6, "0")[:6] + n

# Setup: one customer/lead + two vehicles
p = P("01")
for m in ["hi", "buy", "10 lakh", "Tata", "Nexon", "Diesel", "Manual", "2020"]:
    sim(p, m)
s, leads = api("GET", "/api/leads")
lid = [l for l in leads if l["phone"] == p][0]["id"]
s, v1 = api("POST", "/api/vehicles", {"make": "Race", "model": "One", "year": 2022, "price": 800000})
s, v2 = api("POST", "/api/vehicles", {"make": "Race", "model": "Two", "year": 2022, "price": 800000})

# V3-28: 100 concurrent bookings, same vehicle -> exactly 1 success
with ThreadPoolExecutor(100) as ex:
    res = list(ex.map(lambda i: api("POST", "/api/bookings", {"lead_id": lid, "vehicle_id": v1["id"], "amount": 800000})[0], range(100)))
ok, cf, other = res.count(200), res.count(409), [c for c in res if c not in (200, 409)]
check("V3-28 bookingx100 = 1x200 + 99x409", ok == 1 and cf == 99, f"200={ok} 409={cf} other={other[:5]}")

# V3-27: 100 concurrent TD same slot -> exactly 1 success
with ThreadPoolExecutor(100) as ex:
    res = list(ex.map(lambda i: api("POST", "/api/test-drives", {"lead_id": lid, "vehicle_id": v2["id"], "scheduled_at": "2026-12-01T10:00:00Z"})[0], range(100)))
ok, cf, other = res.count(200), res.count(409), [c for c in res if c not in (200, 409)]
check("V3-27 TDx100 = 1x200 + 99x409", ok == 1 and cf == 99, f"200={ok} 409={cf} other={other[:5]}")

# V3-24: same WA event 100x -> one business action (simulate has no waid;
# direct dup path covered by Go test; here assert lead-count stability)
s, leads = api("GET", "/api/leads")
n0 = len([l for l in leads if l["phone"] == p])
with ThreadPoolExecutor(20) as ex:
    list(ex.map(lambda i: sim(p, "hello"), range(100)))
s, leads = api("GET", "/api/leads")
n1 = len([l for l in leads if l["phone"] == p])
check("V3-24 100x same msg, leads stable", n0 == n1, f"{n0}->{n1}")

# V3-21 burst: 100 rapid distinct messages, one phone
with ThreadPoolExecutor(20) as ex:
    reps = list(ex.map(lambda i: sim(P("02"), ["hi", "buy", "BMW", "X1", "Diesel", "Automatic", "20 lakh", "2020"][i % 8]), range(100)))
s, leads = api("GET", "/api/leads")
mine = [l for l in leads if l["phone"] == P("02")]
check("V3-21 burst one lead, valid state", len(mine) == 1 and mine[0]["state"].startswith("BUY"), (len(mine), mine[0]["state"] if mine else None))

# V3-22 multi-phone burst: 5 phones x 20 msgs, no cross-talk
phones = [P("1" + str(i)) for i in range(5)]
def hit(args):
    ph, i = args
    return sim(ph, ["hi", "buy", "5 lakh", "Maruti"][i % 4])
with ThreadPoolExecutor(25) as ex:
    list(ex.map(hit, [(ph, i) for ph in phones for i in range(20)]))
s, leads = api("GET", "/api/leads")
per = {ph: len([l for l in leads if l["phone"] == ph]) for ph in phones}
check("V3-22 multi-phone isolated", all(v == 1 for v in per.values()), per)

# V3-29: book vs concurrent SOLD — deterministic, never inconsistent
s, v3 = api("POST", "/api/vehicles", {"make": "Race", "model": "Three", "year": 2022, "price": 700000})
import threading
out = {}
def do_book(): out["book"] = api("POST", "/api/bookings", {"lead_id": lid, "vehicle_id": v3["id"], "amount": 700000})[0]
def do_sold(): out["sold"] = api("PATCH", f"/api/vehicles/{v3['id']}", {"status": "SOLD"})[0]
ts = [threading.Thread(target=do_book) for _ in range(5)] + [threading.Thread(target=do_sold) for _ in range(5)]
[t.start() for t in ts]; [t.join() for t in ts]
s, bks = api("GET", "/api/bookings")
s, vehs = api("GET", "/api/vehicles?limit=100")
v3st = [v for v in vehs if v["id"] == v3["id"]][0]["status"]
import subprocess as _sp  # precise per-vehicle count (names repeat across runs)
_nb = _sp.run(["psql", "-h", os.environ.get("PGHOST", "/tmp"), "-p", os.environ.get("PGPORT", "55435"),
    "-d", os.environ.get("PGDATABASE", "sellingbot"), "-tAc",
    f"SELECT COUNT(*) FROM bookings WHERE vehicle_id='{v3['id']}' AND status IN ('PENDING','CONFIRMED')"],
    capture_output=True, text=True).stdout.strip() or "0"
nb = int(_nb)
consistent = (nb <= 1) and not (nb == 1 and v3st == "AVAILABLE")
check("V3-29 book-vs-sold consistent", consistent, f"bookings={nb} vehicle={v3st}")

# V3-26: identical followup twice -> second 409
fb = {"lead_id": lid, "scheduled_at": "2026-12-02T10:00:00Z", "message": f"race-dup-{RUN}", "type": "general"}
s1, _ = api("POST", "/api/followups", fb)
s2, _ = api("POST", "/api/followups", fb)
check("V3-26 dup followup 409", s1 == 200 and s2 == 409, (s1, s2))

print("RACES:", "ALL PASS" if not fails else f"{fails} FAILED")
raise SystemExit(1 if fails else 0)
