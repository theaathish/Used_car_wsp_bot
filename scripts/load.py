#!/usr/bin/env python3
"""V3 load stages 10/25/50/100 + metrics leak check + optional mini-soak.
Usage: python3 load.py [10|25|50|100|all] [--soak-min N]"""
import json, os, statistics, sys, time, urllib.request, urllib.error
from concurrent.futures import ThreadPoolExecutor

BASE = os.environ.get("BASE_URL", "http://127.0.0.1:18082")
TOKEN = open(os.environ.get("TOKEN_FILE", "/tmp/sb-token")).read().strip()
RUN = str(int(time.time()) % 1000000)

def api(method, path, body=None, timeout=30):
    req = urllib.request.Request(BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {TOKEN}"})
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            r.read()
            return time.time() - t0, r.status
    except urllib.error.HTTPError as e:
        return time.time() - t0, e.code
    except Exception:
        return time.time() - t0, -1

def metrics():
    _, m = api("GET", "/api/metrics"), None
    import json as J
    req = urllib.request.Request(BASE + "/api/metrics", headers={"Authorization": f"Bearer {TOKEN}"})
    with urllib.request.urlopen(req, timeout=10) as r:
        return J.loads(r.read())

def mix(i):
    k = i % 10
    if k < 3: return ("GET", "/api/dashboard", None)
    if k < 5: return ("GET", "/api/vehicles?limit=20", None)
    if k < 6: return ("GET", "/api/leads?limit=20", None)
    if k < 7: return ("GET", "/api/health", None)
    if k < 9: return ("POST", "/api/whatsapp/simulate", {"phone": f"5{RUN[-5:]}9{i % 997:03d}", "body": ["hi", "buy", "5 lakh"][i % 3]})
    return ("POST", "/api/vehicles", {"make": "Load", "model": f"M{i % 50}", "year": 2020, "price": 500000})

def stage(n, reqs_per_user=10):
    jobs = [(m, p, b) for _ in range(n) for (m, p, b) in [mix(j) for j in range(reqs_per_user)]]
    # rebuild properly: n users x reqs
    jobs = []
    for u in range(n):
        for j in range(reqs_per_user):
            jobs.append(mix(u * reqs_per_user + j))
    t0 = time.time()
    with ThreadPoolExecutor(n) as ex:
        res = list(ex.map(lambda jb: api(*jb), jobs))
    dt = time.time() - t0
    lats = sorted(ms for ms, _ in res)
    codes = {}
    for _, c in res:
        codes[c] = codes.get(c, 0) + 1
    p = lambda q: lats[min(len(lats) - 1, int(len(lats) * q))] * 1000
    print(f"users={n} reqs={len(jobs)} wall={dt:.1f}s rps={len(jobs)/dt:.0f} "
          f"p50={p(0.5):.0f}ms p95={p(0.95):.0f}ms p99={p(0.99):.0f}ms codes={codes}")
    return codes

which = sys.argv[1] if len(sys.argv) > 1 and not sys.argv[1].startswith("--") else "all"
stages = {"10": 10, "25": 25, "50": 50, "100": 100}
todo = [stages[which]] if which in stages else [10, 25, 50, 100]
m0 = metrics()
print("before:", {k: m0[k] for k in ("goroutines", "db_total_conns", "db_acquired", "db_idle")})
allcodes = {}
for n in todo:
    for c, k in stage(n).items():
        allcodes[c] = allcodes.get(c, 0) + k
bad = {c: k for c, k in allcodes.items() if c not in (200, 409)}
print("error-summary (non-200/409):", bad or "none")
time.sleep(5)
m1 = metrics()
print("after: ", {k: m1[k] for k in ("goroutines", "db_total_conns", "db_acquired", "db_idle")})
print("leak-check:",
      "GROWTH" if m1["goroutines"] > m0["goroutines"] + 10 or m1["db_acquired"] != 0
      else "stable (acquired=0, idle retained by capped pool)")

if "--soak-min" in sys.argv:
    mins = int(sys.argv[sys.argv.index("--soak-min") + 1])
    print(f"mini-soak {mins}min @ ~20rps mixed...")
    end = time.time() + mins * 60
    i = 0
    errs = 0
    with ThreadPoolExecutor(10) as ex:
        while time.time() < end:
            batch = [mix(i + j) for j in range(20)]
            for ms, c in ex.map(lambda jb: api(*jb), batch):
                if c not in (200, 409):
                    errs += 1
            i += 20
            m = metrics()
            print(f"  t+{int(mins*60-(end-time.time()))}s g={m['goroutines']} db={m['db_total_conns']}/{m['db_acquired']} errs={errs}")
            time.sleep(5)
    print("soak errs:", errs)
