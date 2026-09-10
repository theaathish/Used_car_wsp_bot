# QA Checklist — Used Car WhatsApp Automation V1

Principle: test it as a deterministic workflow, not a chatbot —
`INPUT → RULE → STATE CHANGE → DATABASE → WHATSAPP RESPONSE`.
Payment is out of scope (ignored everywhere, incl. finance = info only).

## Automated (run on every change)

```bash
go test ./...                                   # unit: state machine, budget parse, interest
DATABASE_URL=... go test ./internal/whatsapp/  # + WA-004 dedup (live DB)
python3 /tmp/sb-e2e.py                          # 30-case live E2E (see below)
```

Live E2E covers: WA-002/003, BUY-001..010, SELL-001..004, EXCHANGE-001,
VEH-001, MATCH-001/002, TD-001/002/003, FOLLOW-001/002, negotiations,
finance, RBAC/assign (AUTH-004), BOOK-001/002, delivery, review, dashboard,
outbox. Last run: **30/30 pass**.

## V2 deep QA (production hardening) — verified

```bash
go test ./...                                   # state machine + validators
DATABASE_URL=... go test ./internal/whatsapp/  # + WA-004 dedup (live DB)
python3 scripts/e2e_deep.py                     # 30-case deep live E2E: ALL PASS
./scripts/backup.sh && ./scripts/restore.sh     # backup + restore verified
```

Covered live: STATE-004 (corrupt-state recovery via psql-forced state),
STATE-005 back, STATE-006 start-again, STATE-007 takeover silent+stored,
P0-13 intent switch, BUY-104/105/106 formats, Tanglish/emoji, SELL field
validation + photo cap, INT-003/004 bursts, transition guards (+force),
upload sniff/dedup/cap, metrics, disk %, audit rows, 2h reminder tick,
double-book/slot 409s, RBAC scoping, restore row-count match.

## V3 destruction results (measured locally)

- Races (`scripts/races.py`): bookingx100 = 1x200+99x409, TDx100 same,
  100x same-msg leads stable, 100-msg burst one lead, 5-phone burst
  isolated, book-vs-SOLD deterministic, dup followup 409. ALL PASS.
- Kills: SIGKILL mid-burst -> atomic (0 bookings + AVAILABLE), DB
  stop/start -> controlled 500s, recovery + integrity PASS, stuck
  'sending' claim -> sent exactly once after restart.
- Security (`scripts/security.py`): brute-force 429 + ban, JWT
  expired/wrong-sig/none/malformed rejected, 200+ fuzz cases zero 500s
  (after adding UUID/RFC3339 guards). ALL PASS.
- Session: corrupt whatsapp.db -> stub mode, API healthy, no crash loop.
- Backup: corrupt restore exits loud non-zero; retention keeps newest 7,
  never deletes all; functional restore row counts match.
- Load (`scripts/load.py`, local PG): 10/25/50/100 users, p99 83-86ms
  @100 users, zero errors; pool capped at 20, acquired returns to 0.
- Soak: 3-min mini-soak flat (g=8, db=20/0, 0 errs). 24/48h/7d soaks
  must run on the VPS (`--soak-min 1440` sampling /api/metrics).
- Integrity (`scripts/integrity_check.py`): PASS (13 checks).
- Golden (`scripts/golden.py`): ALL PASS. Regression + 600-combo state
  fuzz: `go test ./...` green.

## V3 sign-off (hard gates)

Automated here: [x] API crash recovery [x] PG restart recovery
[x] outbox/follow-up/reminder crash recovery [x] 100x booking/TD races
[x] burst tests [x] backup->restore works [x] zero dup booking/slot/send
[x] zero state corruption (fuzz+integrity) [x] zero data loss (kills)
[x] no critical security bypass (fuzz+JWT+429).
Manual on YOUR infra: [ ] real QR pair/reconnect/logout [ ] VPS reboot
drill [ ] 24h (preferably 48h) soak [ ] disk-full drill [ ] log rotation.

## Manual checklist (before production)

WHATSAPP — [ ] QR login (`/api/whatsapp/qr`) [ ] session survives restart
(`/data/whatsapp.db` on volume) [ ] incoming/outgoing [ ] media saves to
`/data/images` [ ] reconnect after net drop [ ] duplicate (`WA-004` test)

CUSTOMER/LEADS — [ ] create/find by phone, no dupes [ ] source recorded
[ ] assign salesperson (admin) [ ] sales sees only assigned [ ] statuses

BUY — [ ] budget/brand/model/fuel/trans/year [ ] "don't know" skips to sales
help [ ] one-message multi-field [ ] match display [ ] no-match + similar
[ ] more cars pages forward

SELL — [ ] brand/model/year/reg/km/fuel/trans/condition/location/photos
[ ] `sell_requests` row `VALUATION_PENDING` [ ] sales handoff

EXCHANGE — [ ] current car → new requirement → valuation + matching

INVENTORY — [ ] add/edit/images [ ] AVAILABLE→RESERVED→DELIVERED
[ ] sold never recommended

TEST DRIVE — [ ] slot conflict → 409 + alternative message [ ] 24h + 2h
reminders [ ] reminders survive restart (no double-send)

FOLLOW-UP — [ ] INTERESTED/THINKING/NOT_INTERESTED [ ] scheduled queue
[ ] scheduler marks sent

BOOKING — [ ] concurrent double-book → one 409 [ ] vehicle locks RESERVED
[ ] cancel path

DELIVERY — [ ] booking COMPLETED, vehicle DELIVERED, lead CONVERTED
[ ] review + referral stored

ADMIN/AUTH/INFRA — [ ] login/JWT/401/403 roles/logout [ ] SQLi safe
(parameterized) [ ] Docker `restart: unless-stopped` [ ] Postgres restart
reconnects (pgxpool) [ ] VPS restart order [ ] daily `pg_dump` + restore
drill [ ] Railway volume holds `/data`
