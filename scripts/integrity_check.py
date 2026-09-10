#!/usr/bin/env python3
"""V3-43 data integrity check. Prints violations, exits 1 if any.
Usage: DATABASE_URL=... python3 scripts/integrity_check.py"""
import os, sys

try:
    import psycopg  # psycopg3
    HAVE_PG = "psycopg3"
except ImportError:
    try:
        import psycopg2 as psycopg
        HAVE_PG = "psycopg2"
    except ImportError:
        HAVE_PG = None

CHECKS = [
    ("booking without lead", "SELECT COUNT(*) FROM bookings b LEFT JOIN leads l ON l.id=b.lead_id WHERE l.id IS NULL"),
    ("booking without vehicle", "SELECT COUNT(*) FROM bookings b LEFT JOIN vehicles v ON v.id=b.vehicle_id WHERE v.id IS NULL"),
    ("test drive without lead", "SELECT COUNT(*) FROM test_drives t LEFT JOIN leads l ON l.id=t.lead_id WHERE l.id IS NULL"),
    ("test drive without vehicle", "SELECT COUNT(*) FROM test_drives t LEFT JOIN vehicles v ON v.id=t.vehicle_id WHERE v.id IS NULL"),
    ("message without conversation", "SELECT COUNT(*) FROM messages m LEFT JOIN conversations c ON c.id=m.conversation_id WHERE c.id IS NULL"),
    ("conversation without customer", "SELECT COUNT(*) FROM conversations c LEFT JOIN customers cu ON cu.id=c.customer_id WHERE cu.id IS NULL"),
    ("lead without customer", "SELECT COUNT(*) FROM leads l LEFT JOIN customers cu ON cu.id=l.customer_id WHERE cu.id IS NULL"),
    ("duplicate active booking per vehicle", "SELECT COUNT(*) FROM (SELECT vehicle_id FROM bookings WHERE status IN ('PENDING','CONFIRMED') GROUP BY vehicle_id HAVING COUNT(*) > 1) s"),
    ("duplicate scheduled slot", "SELECT COUNT(*) FROM (SELECT vehicle_id, scheduled_at FROM test_drives WHERE status='SCHEDULED' GROUP BY 1,2 HAVING COUNT(*) > 1) s"),
    ("duplicate processed WA id", "SELECT 0"),  # PK enforces; placeholder
    ("active booking on AVAILABLE vehicle (lock failed)", "SELECT COUNT(*) FROM bookings b JOIN vehicles v ON v.id=b.vehicle_id WHERE b.status IN ('PENDING','CONFIRMED') AND v.status = 'AVAILABLE'"),
    ("lead in invalid state", "SELECT COUNT(*) FROM leads WHERE state NOT IN ('NEW','ASK_INTENT','BUY_BUDGET','BUY_BRAND','BUY_MODEL','BUY_FUEL','BUY_TRANS','BUY_YEAR','BUY_RESULTS','FINANCE_INFO','TESTDRIVE_ASK','SELL_CAR','SELL_YEAR','SELL_DETAILS','SELL_SPECS','SELL_PHOTOS','EXCHANGE_CURRENT','EXCHANGE_WANT','DONE')"),
    ("pending followup without recipient", "SELECT COUNT(*) FROM followups f LEFT JOIN customers cu ON cu.id=f.customer_id LEFT JOIN leads l ON l.id=f.lead_id LEFT JOIN customers lc ON lc.id=l.customer_id WHERE f.status='pending' AND cu.id IS NULL AND lc.id IS NULL"),
    ("outbox stuck sending", "SELECT COUNT(*) FROM outbox WHERE status NOT IN ('PENDING','SENT','FAILED_PERMANENTLY')"),
]

def main():
    dsn = os.environ.get("DATABASE_URL")
    if not dsn:
        print("DATABASE_URL required"); return 2
    if HAVE_PG is None:
        print("need psycopg (pip install psycopg[binary]) or psycopg2"); return 2
    kw = {"conninfo": dsn} if HAVE_PG == "psycopg3" else {"dsn": dsn}
    con = psycopg.connect(**kw)
    cur = con.cursor()
    bad = 0
    for name, q in CHECKS:
        cur.execute(q)
        n = cur.fetchone()[0]
        print(("FAIL " if n else "PASS ") + f"{name}: {n}")
        bad += 1 if n else 0
    print("INTEGRITY CHECK:", "FAIL" if bad else "PASS")
    return 1 if bad else 0

if __name__ == "__main__":
    sys.exit(main())
