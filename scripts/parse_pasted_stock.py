#!/usr/bin/env python3
"""Parse pasted SDAS AS STOCK flat text into import CSV.

The PDF copy-paste collapses columns (REG NUM + CHASSIS often merge
like SB8566FPM118AK040Y613124). This parses with anchors:
  STOCK_NO ... YOM ... REG_DATE ... MILEAGE ... STATUS PRICE ...
Usage:
  python3 scripts/parse_pasted_stock.py stock_raw.txt stock.csv
Then:
  python3 scripts/import_sdas.py --api <url> --email <e> --password <p> --csv stock.csv
Re-running import never duplicates: upsert is keyed by stock_no.
"""
import csv
import re
import sys

LOCATIONS = [
    "Ara Open Car Park", "Courtesy Bukit Jalil", "C&C Glenmarie", "PMM Damansara",
    "SDAS Tebrau", "SDAS Penang", "SDAS Lot 33", "SDAS Tebrau", "SDAS Ara",
    "SDAS CSL", "SDAS JB", "SDAS KL", "SDAS BK", "SDAS Ara", "SDAS CSL",
    "SDAS CSL", "SDAS JB", "SDAS KL", "SDAS BK", "SDAS Ara", "SDAS CSL",
    "RATC ABAD", "CSL B&P", "AB Motorrad", "BYD TREC", "BYD Ara",
    "MINI Ara", "Volvo Ara", "Incoming Stock", "Carrocare", "Mzone",
    "Carsome", "ABBK", "ABKL", "ABAD", "JVC",
]
LOCATIONS = sorted(set(LOCATIONS), key=len, reverse=True)

BRANDS = [
    "Mercedes Benz", "Mercede Benz", "Land Rover",
    "BMW", "MINI", "Audi", "BYD", "Chery", "Denza", "Ford", "Honda",
    "Hyundai", "Jaecoo", "Jaguar", "KIA", "Lexus", "Mazda", "Mitsubishi",
    "Nissan", "Perodua", "Peugeot", "Proton", "Smart", "Subaru", "Suzuki",
    "Tesla", "Toyota", "Volkswagen", "Volvo",
]

CODE_RE = re.compile(r"^(-|[A-Z]{1,2}\d{1,2}[A-Z]?)$")
YEAR_RE = re.compile(r"^(19|20)\d{2}$")
DATE_RE = re.compile(r"^\d{1,2}[./]\d{1,2}[./]\d{2,4}$")
NUM_RE = re.compile(r"^[\d,]+$")
SMALLNUM_RE = re.compile(r"^\d+(\.\d+)?$")
PLATE_FULL_RE = re.compile(r"^[A-Z]{1,3}\d{1,4}[A-Z]{0,3}$")


def split_merged_reg(tok, next_tok):
    """Split merged REGNUM+CHASSIS (e.g. SB8566FPM118AK040Y613124).
    Rule 1: merged starts with the following OLD REG token.
    Rule 2: chassis is 16-17 chars (VIN-like); reg must be a full plate.
    Returns (reg, chassis) or (None, None)."""
    if next_tok and tok.startswith(next_tok) and len(tok) - len(next_tok) >= 14:
        return next_tok, tok[len(next_tok):]
    for cl in (17, 16):
        if len(tok) > cl + 2:
            reg, ch = tok[: len(tok) - cl], tok[len(tok) - cl:]
            if (len(reg) <= 8 and PLATE_FULL_RE.match(reg)
                    and ch[0].isalpha() and ch.isalnum()):
                return reg, ch
    return None, None

UPHOLSTERY_MULTI = {
    "Veganza Perforated Black", "Vernasca Black", "Vernasca Mocha",
    "Vernasca Dark Truffle", "Vernasca Tacora Red",
    "Merino Black", "Merino Tartufo", "Merino Amarone",
    "Merino Smoke White", "Merino Copper Brown", "Merino Silverstone",
    "Merino Marina Blue", "Merino Deep Lagoon", "Merino Dark Truffle",
    "Leather Tartufo",
    "Merino Copper Brown / Atlas Grey", "Merino Silverstone / Atlas Grey",
    "Vescin Dark Petrol", "Vescin Nightshade Blue", "Vescin Vintage Brown",
    "Vescin Petrol Dark",
    "Leather Merino Black", "Leather Merino Copper Brown",
    "Leather Merino Silverstone", "Leather Merino Silverstone II",
    "Leather Merino Black / Atlas Grey",
    "Leather Merino Copper Brown / Atlas Grey",
    "Leather Merino Silverstone / Atlas Grey",
    "Leather Merino Silverstone II / Atlas Grey",
    "Lthr. Cross Punch Black Carbon", "Lthr. Cross Punch Black",
    "Cross Punch Black Carbon", "Light Chequered", "Carbon Black",
    "Coral Red", "Espresso Brown", "Atelier Black", "Atelier Mocha",
    "Suite Amido", "Copper Brown", "Dark Petrol", "Vintage Brown",
    "Petrol Dark", "Nightshade Blue", "Tacora Red", "Dark Truffle",
    "Perforated Black", "Deep Lagoon", "Smoke White", "Silverstone",
    "Espresso", "Chequered",
}
UPHOLSTERY_SINGLE = {
    "Black", "Mocha", "Beige", "Brown", "White", "Red", "Cognac",
    "Silver", "Blue", "Grey", "Terra", "Green", "Coffee",
}

WARRANTIES = [
    "Remaining 5 Years Warranty + Additional 5 Years Drivecare Service",
    "Remaining 5 Years Warranty",
    "Remaining 4 Years Warranty",
    "Optional to Purchase SDAS EWP + FOC 1 time Drivecare Service",
    "SDAS PHEV EWP + FOC 1 time Drivecare Service",
    "As It Is Basis (No Warranty), no refurbishment + FOC 1 time Drivecare Service",
    "As It Is Basis (No Warranty) + FOC 1 time Drivecare Service",
    "As It Is Basis",
    "No manufacturing warranty, FOC 3 years GMR warranty + FOC 1 time Drivecare Service",
    "FOC 2 Years SDAS EWP + FOC 1 time Drivecare Service",
    "Optional to Purchase SDAS EWP + RM2,000 Diesel e-Voucher + FOC 1 time Drivecare Service",
    "Refer to service booklet + RM2,000 Diesel e-Voucher",
    "With 1 Year C&C Extended Warranty",
    "Refer to service booklet",
    "Refer to Motorrad",
]
WARRANTIES = sorted(WARRANTIES, key=len, reverse=True)

SKIP_PREFIXES = ("Restricted", "STRICTLY", "STOCK NO", "AS STOCK", "#")


def parse_line(line, lineno):
    line = re.sub(r"^[^A-Za-z0-9]+", "", line.strip())
    if not line or line.startswith(SKIP_PREFIXES):
        return None
    if "STOCK LOCATION" in line and "MODEL" in line:
        return None
    m = re.search(r"\b(ALLOCATED|FREESTOCK)\b", line)
    if not m:
        return ("reject", f"line {lineno}: no STATUS")
    status = m.group(1)
    left, right = line[: m.start()], line[m.end():]

    # Right side: price, optional small numeric cols, warranty, claims.
    rtoks = right.split()
    if not rtoks or not NUM_RE.match(rtoks[0]):
        return ("reject", f"line {lineno}: bad price")
    price = int(rtoks[0].replace(",", ""))
    i = 1
    while i < len(rtoks) and SMALLNUM_RE.match(rtoks[i]):
        # campaign/key-count column, not needed
        i += 1
    rest = " ".join(rtoks[i:])
    warranty, claims = "", rest
    rl = rest.lower()
    for w in WARRANTIES:
        if rl.startswith(w.lower()):
            warranty, claims = rest[: len(w)], rest[len(w):].strip()
            break
    if not warranty:
        return ("reject", f"line {lineno}: unknown warranty in: {rest[:60]}")

    toks = left.split()
    if not toks:
        return ("reject", f"line {lineno}: empty left")
    # Stock no (or rows without one: location-first, or Incoming Stock).
    stock_no, pos, location = "", 0, ""
    if toks[0] == "Incoming" and len(toks) > 1 and toks[1] == "Stock":
        pos = 2
        location = "Incoming Stock"
    elif toks[0][0].isdigit():
        stock_no, pos = toks[0], 1
    for loc in LOCATIONS:
        parts = loc.split()
        if toks[pos: pos + len(parts)] == parts:
            location = loc
            pos += len(parts)
            break
    if not location:
        return ("reject", f"line {lineno}: unknown location at: {' '.join(toks[pos:pos+3])}")

    # Brand + model description up to MODEL_CODE/YOM.
    brand = ""
    for b in sorted(BRANDS, key=len, reverse=True):
        parts = b.split()
        if toks[pos: pos + len(parts)] == parts:
            brand = b
            pos += len(parts)
            break
    if not brand:
        return ("reject", f"line {lineno}: unknown brand at: {' '.join(toks[pos:pos+4])}")
    # YOM anchor: first 4-digit year whose PREVIOUS token is the model code
    # (model names like Peugeot 2008 contain year-like numbers).
    yi = None
    for j in range(pos, len(toks)):
        if YEAR_RE.match(toks[j]) and j - pos >= 2 and CODE_RE.match(toks[j - 1]):
            yi = j
            break
    if yi is None:
        return ("reject", f"line {lineno}: no YOM/model code")
    model_code = toks[yi - 1]
    if not CODE_RE.match(model_code):
        return ("reject", f"line {lineno}: bad model code: {model_code}")
    model_desc = brand + " " + " ".join(toks[pos: yi - 1])
    yom = int(toks[yi])
    if not (1990 <= yom <= 2027):
        return ("reject", f"line {lineno}: bad YOM {yom}")
    pos = yi + 1

    # REG NUM [+CHASSIS merged], ["(Num Retain)"], CHASSIS, OLD REG, [PURCHASER], DATE, MILEAGE.
    reg_num = chassis = None
    if pos < len(toks) and len(toks[pos]) > 10:
        nxt = toks[pos + 1] if pos + 1 < len(toks) else ""
        reg_num, chassis = split_merged_reg(toks[pos], nxt)
        if reg_num is None:
            return ("reject", f"line {lineno}: bad merged reg/chassis")
        pos += 1
    if reg_num is None:
        if pos + 1 >= len(toks):
            return ("reject", f"line {lineno}: truncated reg/chassis")
        reg_num = toks[pos]
        pos += 1
        while pos < len(toks) and toks[pos].startswith("("):
            while pos < len(toks) and not toks[pos].endswith(")"):
                pos += 1
            pos += 1  # skip "(Num Retain)" style parentheticals
        if pos >= len(toks):
            return ("reject", f"line {lineno}: truncated chassis")
        chassis = toks[pos]
        pos += 1
    if pos >= len(toks):
        return ("reject", f"line {lineno}: truncated old reg")
    old_reg = toks[pos]
    pos += 1
    purchaser = ""
    if pos < len(toks) and not DATE_RE.match(toks[pos]):
        purchaser = toks[pos]
        pos += 1
    if pos >= len(toks) or not DATE_RE.match(toks[pos]):
        return ("reject", f"line {lineno}: bad reg date")
    reg_date = toks[pos]
    pos += 1
    if pos >= len(toks) or not NUM_RE.match(toks[pos]):
        return ("reject", f"line {lineno}: bad mileage")
    mileage = int(toks[pos].replace(",", ""))
    pos += 1

    # Colour + upholstery = remaining left tokens; longest upholstery suffix wins.
    tail = toks[pos:]
    if not tail:
        return ("reject", f"line {lineno}: missing colour/upholstery")
    upholstery, colour = tail[-1], " ".join(tail[:-1])
    for n in range(min(len(tail), 7), 0, -1):
        cand = " ".join(tail[-n:])
        if cand in UPHOLSTERY_MULTI or (n == 1 and cand in UPHOLSTERY_SINGLE):
            upholstery, colour = cand, " ".join(tail[:-n])
            break
    if upholstery == "Balck":
        upholstery = "Black"
    if not stock_no:
        if not reg_num:
            return ("reject", f"line {lineno}: no stock_no and no reg_num")
        stock_no = ("INCOMING-" if location == "Incoming Stock" else "NOSTOCK-") + reg_num
    return ("ok", {
        "stock_no": stock_no, "stock_location": location,
        "model_description": model_desc, "model_code": "" if model_code == "-" else model_code,
        "yom": yom, "reg_num": reg_num, "chassis": chassis,
        "old_reg_num": old_reg, "purchaser": purchaser, "reg_date": reg_date,
        "mileage": mileage, "colour": colour, "upholstery": upholstery,
        "status": status, "selling_price": price,
        "warranty": warranty, "claims": claims,
    })


def main():
    if len(sys.argv) != 3:
        print("usage: parse_pasted_stock.py stock_raw.txt stock.csv", file=sys.stderr)
        return 2
    rows, rejects = [], []
    with open(sys.argv[1], encoding="utf-8-sig") as f:
        for n, line in enumerate(f, 1):
            if not line.strip():
                continue
            r = parse_line(line, n)
            if r is None:
                continue
            if r[0] == "ok":
                rows.append(r[1])
            else:
                rejects.append(r[1])
    headers = ["stock_no", "stock_location", "model_description", "model_code",
               "yom", "reg_num", "chassis", "old_reg_num", "purchaser",
               "reg_date", "mileage", "colour", "upholstery", "status",
               "selling_price", "warranty", "claims"]
    with open(sys.argv[2], "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=headers)
        w.writeheader()
        w.writerows(rows)
    free = sum(1 for r in rows if r["status"] == "FREESTOCK")
    print(f"parsed={len(rows)} freestock={free} allocated={len(rows)-free} rejected={len(rejects)} -> {sys.argv[2]}")
    for r in rejects[:20]:
        print("REJECT: " + r, file=sys.stderr)
    return 0 if rows else 1


if __name__ == "__main__":
    raise SystemExit(main())
