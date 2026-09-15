#!/usr/bin/env python3
"""10-persona English flow simulation via POST /api/whatsapp/simulate.
Each persona gets its own phone so state is isolated.
Usage: python3 scripts/simulate_personas.py --api http://localhost:18083 --email admin@local.test --password admin123
Checks: every reply non-empty; reports logic breaks at the end.
"""
import argparse, json, urllib.request

PERSONAS = [
    ("1/Happy-Path Buyer", "60110000011", [
        "hi", "BUY", "RM 100000", "BMW", "any", "any", "any", "any",
        "1", "YES",
    ]),
    ("2/Unsure-Budget Buyer", "60110000012", [
        "hello", "I want to buy a car", "I don't know", "MINI", "any", "any", "any", "any",
    ]),
    ("3/Premium One-Liner", "60110000013", [
        "Hi", "I am looking for a BMW X1 under 200k", "any", "any", "any",
    ]),
    ("4/Seller Full Flow", "60110000014", [
        "hi", "SELL", "Swift VDI", "2018, MH12AB1234, 55000km",
        "Diesel, Manual, Good, Pune", "DONE",
    ]),
    ("5/Exchanger + Test Drive", "60110000015", [
        "hi", "EXCHANGE", "Alto 2016, 60000km", "Creta under 10 lakh",
        "test drive", "1, tomorrow 10am",
    ]),
    ("6/Browser Paging + Bad Number", "60110000016", [
        "BUY", "150000", "any", "any", "any", "any", "any",
        "more cars", "9", "1", "more photos",
    ]),
    ("7/Finance Seeker", "60110000017", [
        "hi", "BUY", "RM 120000", "I need finance help",
        "5 lakh, 60 months, salaried, 80000",
    ]),
    ("8/Mind-Changer Menu+Switch", "60110000018", [
        "hi", "BUY", "RM 90000", "menu", "SELL",
        "Swift VDI", "actually I want to buy a car",
    ]),
    ("9/Deliberator Thinking", "60110000019", [
        "hi", "BUY", "100000", "BMW", "any", "any", "any", "any",
        "I need to think about it",
    ]),
    ("10/Drop-off + Restart", "60110000020", [
        "hi", "BUY", "80000", "not interested", "BUY", "RM 100000",
    ]),
]

EXPECTED = {
    # phone -> list of substrings that must appear in order across the transcript
    "60110000011": ["What's your budget", "Which brand", "Top picks", "Like it?", "salesperson will call"],
    "60110000012": ["sales team can help with budget", "Top picks"],
    "60110000014": ["manufacturing year", "fuel, transmission", "send car photos", "VALUATION_PENDING"],
    "60110000015": ["current car", "What new car", "valuation", "test drive", "Test drive confirmed"],
    "60110000016": ["More options", "isn't on the list", "Like it?"],
    "60110000017": ["loan assistance", "finance team will contact"],
    "60110000018": ["starting fresh", "Which car do you want to sell", "you want to *BUY*"],
    "60110000019": ["take your time", "follow up"],
    "60110000020": ["No problem", "What's your budget"],
}

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
    fails, logic = 0, []
    for name, phone, msgs in PERSONAS:
        # "menu" first makes reruns idempotent: resets any stale BUY_RESULTS/DONE state
        msgs = ["menu"] + msgs
        print(f"\n===== {name} [{phone}] =====")
        transcript = []
        for m in msgs:
            try:
                res = api(base, "/api/whatsapp/simulate", token, {"phone": phone, "name": name, "body": m})
                reply = res.get("reply") or ""
                transcript.append(reply)
                flat = reply.replace("\n", " / ")
                flag = "" if reply else "  <-- EMPTY REPLY (FAIL)"
                if not reply:
                    fails += 1
                print(f"HUMAN: {m}\nBOT:   {flat[:500]}{flag}\n")
            except Exception as e:
                fails += 1
                transcript.append("")
                print(f"HUMAN: {m}\nBOT:   ERROR {e}\n")
        for want in EXPECTED.get(phone, []):
            if not any(want.lower() in (t or "").lower() for t in transcript):
                logic.append(f"{name}: missing expected '{want}'")
                print(f"  !! LOGIC BREAK: expected '{want}' never appeared")
    print(f"\nPERSONAS DONE: {len(PERSONAS)} personas, empty/error replies: {fails}, logic breaks: {len(logic)}")
    for l in logic:
        print(" - " + l)
    return 1 if (fails or logic) else 0

if __name__ == "__main__":
    raise SystemExit(main())
