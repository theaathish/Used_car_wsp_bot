#!/usr/bin/env python3
"""Persona flow simulation via POST /api/whatsapp/simulate.
Each persona gets its own phone so state is isolated.
Usage: python3 scripts/simulate_personas.py --api http://localhost:18083 --email admin@local.test --password admin123
"""
import argparse, json, urllib.request

PERSONAS = [
    ("Budget Buyer (proper English)", "60110000001", [
        "hi", "BUY", "RM 90000", "BMW", "any", "any", "any", "any", "more cars", "1",
    ]),
    ("Tanglish Casual (typos, Tamil mix)", "60110000002", [
        "hai machi", "buy", "budget theriyala", "bmw venum", "any da", "petrol", "automatic", "any",
    ]),
    ("Premium One-liner (multi-field)", "60110000003", [
        "Hello", "I want to buy BMW X1 under 200k", "any", "any", "any",
    ]),
    ("Seller (sell flow)", "60110000004", [
        "hi", "SELL", "Swift VDI", "2018, MH12AB1234, 55000km", "Diesel, Manual, Good, Pune", "DONE",
    ]),
    ("Exchanger + haggler", "60110000005", [
        "hi", "EXCHANGE", "Alto 2016, 60000km", "Creta under 10 lakh", "test drive", "1, tomorrow 10am",
    ]),
    ("Impatient tapper (numbers/short)", "60110000006", [
        "BUY", "100000", "any", "any", "any", "any", "any", "1", "more photos", "YES",
    ]),
]

def api(base, path, token, payload):
    req = urllib.request.Request(base + path, data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode())

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", required=True)
    ap.add_argument("--email", required=True)
    ap.add_argument("--password", required=True)
    a = ap.parse_args()
    base = a.api.rstrip("/")
    req = urllib.request.Request(base + "/api/auth/login", data=json.dumps({"email": a.email, "password": a.password}).encode(), headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        token = json.loads(r.read().decode())["token"]
    fails = 0
    for name, phone, msgs in PERSONAS:
        print(f"\n===== {name} [{phone}] =====")
        for m in msgs:
            try:
                res = api(base, "/api/whatsapp/simulate", token, {"phone": phone, "name": name, "body": m})
                reply = (res.get("reply") or "").replace("\n", " / ")
                flag = "" if reply else "  <-- EMPTY REPLY (FAIL)"
                if not reply:
                    fails += 1
                print(f"HUMAN: {m}\nBOT:   {reply[:500]}{flag}\n")
            except Exception as e:
                fails += 1
                print(f"HUMAN: {m}\nBOT:   ERROR {e}\n")
    print(f"\nPERSONAS DONE, empty/error replies: {fails}")
    return 1 if fails else 0

if __name__ == "__main__":
    raise SystemExit(main())
