#!/usr/bin/env python3
"""V3 kill battery. Requires harness env: BASE_URL, TOKEN_FILE, PGHOST,
PGPORT, PGDATABASE. Server/PG are managed by the caller via start/stop hooks
passed as argv: python3 kill.py --server-cmd "..." (ran in background by caller).

Simpler contract used here: the test only GENERATES load + verifies state.
Killing/restarting is done by the shell driver between phases.
Phases (shell): seed -> kill -9 api mid-burst -> up -> db stop mid-traffic -> up.
"""
import json, os, time, urllib.request, urllib.error
from concurrent.futures import ThreadPoolExecutor

BASE = os.environ.get("BASE_URL", "http://127.0.0.1:18082")
TOKEN = open(os.environ.get("TOKEN_FILE", "/tmp/sb-token")).read().strip()
RUN = str(int(time.time()) % 1000000)
fails = []

def api(method, path, body=None, timeout=30):
    req = urllib.request.Request(BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {TOKEN}"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        return e.code, None
    except Exception as e:
        return -1, repr(e)[:100]

def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name, str(extra)[:120])
    if not cond:
        fails.append(name)

phase = os.environ.get("KILL_PHASE", "seed")

if phase == "seed":
    P = lambda n: "6" + RUN.rjust(6, "0")[:6] + n
    for i in range(5):
        for m in ["hi", "buy", "5 lakh"]:
            api("POST", "/api/whatsapp/simulate", {"phone": P(f"0{i}"), "body": m})
    s, v = api("POST", "/api/vehicles", {"make": "Kill", "model": "Test", "year": 2022, "price": 500000})
    open("/tmp/sb-kill-vid", "w").write(v["id"])
    s, leads = api("GET", "/api/leads")
    open("/tmp/sb-kill-lead", "w").write([l for l in leads if l["phone"] == P("00")][0]["id"])
    s, f = api("POST", "/api/followups", {"lead_id": open("/tmp/sb-kill-lead").read().strip(),
        "scheduled_at": "2026-01-01T00:00:00Z", "message": f"killtest-{RUN}", "type": "general"})
    check("K-seed", s == 200, (s, f))
elif phase == "burst":
    # fire-and-forget booking burst for the shell to SIGKILL mid-flight
    vid, lid = open("/tmp/sb-kill-vid").read().strip(), open("/tmp/sb-kill-lead").read().strip()
    with ThreadPoolExecutor(50) as ex:
        list(ex.map(lambda i: api("POST", "/api/bookings", {"lead_id": lid, "vehicle_id": vid, "amount": 1}), range(50)))
        list(ex.map(lambda i: api("POST", "/api/whatsapp/simulate", {"phone": f"66{RUN[-5:]}0{i % 5}", "body": "hi"}), range(50)))
    print("BURST_DONE")
elif phase == "verify":
    import subprocess
    q = lambda sql: subprocess.run(["psql", "-h", os.environ.get("PGHOST", "/tmp"), "-p",
        os.environ.get("PGPORT", "55435"), "-d", os.environ.get("PGDATABASE", "sellingbot"), "-tAc", sql],
        capture_output=True, text=True).stdout.strip()
    vid = open("/tmp/sb-kill-vid").read().strip()
    n = int(q(f"SELECT COUNT(*) FROM bookings WHERE vehicle_id='{vid}' AND status IN ('PENDING','CONFIRMED')") or 0)
    vst = q(f"SELECT status FROM vehicles WHERE id='{vid}'")
    check("K-verify booking atomic", (n == 1 and vst in ("RESERVED", "BOOKED", "SOLD", "DELIVERED")) or (n == 0 and vst == "AVAILABLE"), f"n={n} vehicle={vst}")
    s, h = api("GET", "/api/health")
    check("K-verify api healthy", s == 200 and (h or {}).get("db") is True, h)
    s, m = api("GET", "/api/metrics")
    check("K-verify metrics", s == 200, m)

print("KILL:", "ALL PASS" if not fails else f"{fails} FAILED")
raise SystemExit(1 if fails else 0)
