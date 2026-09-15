#!/usr/bin/env python3
"""Import SDAS AS STOCK sheet into SellingBot.
CSV headers (case-insensitive, accepts PDF header names):
  stock_no,stock_location,model_description,model_code,yom,reg_num,chassis,old_reg_num,
  purchaser,reg_date,mileage,colour,upholstery,status,selling_price,warranty,claims

Usage:
  python3 scripts/import_sdas.py --api https://usedcarwspbot-production.up.railway.app \
    --email admin@local.test --password admin123 --csv stock.csv [--dry-run]

Converts PDF rows to POST /api/vehicles/import (bulk upsert by stock_no).
FREESTOCK->AVAILABLE (matchable on WhatsApp), ALLOCATED->RESERVED (hidden).
"""
import argparse, csv, json, sys, urllib.request

LOGIN = "/api/auth/login"
IMPORT = "/api/vehicles/import"

def api_post(base, path, token, payload):
    req = urllib.request.Request(base + path, data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", **({"Authorization": f"Bearer {token}"} if token else {})})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read().decode())

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", required=True)
    ap.add_argument("--email", required=True)
    ap.add_argument("--password", required=True)
    ap.add_argument("--csv", required=True)
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args()
    base = a.api.rstrip("/")

    with open(a.csv, newline="", encoding="utf-8-sig") as f:
        reader = csv.DictReader(f)
        # normalize headers: lower, spaces->underscores
        rows = []
        for r in reader:
            n = {(k or "").strip().lower().replace(" ", "_"): (v or "").strip() for k, v in r.items()}
            if not n.get("stock_no"):
                continue
            rows.append({
                "stock_no": n.get("stock_no", ""),
                "stock_location": n.get("stock_location", ""),
                "model_description": n.get("model_description", ""),
                "model_code": n.get("model_code", ""),
                "yom": n.get("yom", ""),
                "reg_num": n.get("reg_num", ""),
                "chassis": n.get("chassis", ""),
                "old_reg_num": n.get("old_reg_num", ""),
                "purchaser": n.get("purchaser", ""),
                "reg_date": n.get("reg_date", ""),
                "mileage": n.get("mileage", ""),
                "colour": n.get("colour", ""),
                "upholstery": n.get("upholstery", ""),
                "stock_status": n.get("status", n.get("stock_status", "")).upper(),
                "selling_price": n.get("selling_price", ""),
                "warranty": n.get("warranty", ""),
                "claims": n.get("claims", ""),
            })
    print(f"parsed {len(rows)} rows from {a.csv}")
    if a.dry_run or not rows:
        print(json.dumps(rows[:3], indent=1)[:2000])
        return
    tok = api_post(base, LOGIN, None, {"email": a.email, "password": a.password})["token"]
    # chunk 500 rows per request
    total = 0
    for i in range(0, len(rows), 500):
        chunk = rows[i:i+500]
        res = api_post(base, IMPORT, tok, chunk)
        total += res.get("upserted", 0)
        print(f"chunk {i//500+1}: {res}")
    print(f"DONE upserted={total}")

if __name__ == "__main__":
    main()
