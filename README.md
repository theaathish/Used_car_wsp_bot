# SellingBot V1 — single-service Railway deploy

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template/SELLINGBOT_TEMPLATE_CODE)

Go monolith: REST API + WhatsApp worker (whatsmeow) + 60s follow-up scheduler + embedded admin UI. One service + Railway Postgres + 0.5GB volume.

> Deploy button: replace `SELLINGBOT_TEMPLATE_CODE` with the published
> template code after following `docs/railway-template.md` (one template
> publish, then every deploy is one click).

## 1-click Railway deploy (zero manually-set env vars)

Set **nothing** — the app boots with safe defaults:

- `JWT_SECRET` unset → generated once, persisted to `/data/jwt.secret`
  (set `JWT_SECRET` only to pin/rotate it).
- `ADMIN_SEED_PASSWORD` unset → random password generated at first boot and
  printed **once** in the deploy logs as
  `FIRST BOOT — admin created. Login: <email> / <password>`.
  Default email `admin@autokart.local` (pin via `ADMIN_SEED_EMAIL` /
  `ADMIN_SEED_PASSWORD` if you want fixed credentials).
- `DATABASE_URL` is injected by Postgres (template or IaC below).

Pick one path:

**A. Template (one click, recommended):** click Deploy above. It provisions
`sellingbot` + `postgres` + `/data` volume with `DATABASE_URL` wired.
See `docs/railway-template.md` for the Template Composer source of truth.

**B. Railway IaC (`railway config apply`):**

```bash
npm install railway
railway link
railway config plan    # preview
railway config apply   # provisions app + postgres + volume per .railway/railway.ts
```

**C. Manual:** push to GitHub → Railway → New Project → Deploy from Repo →
add PostgreSQL → add Volume mounted at `/data` → deploy (no variables
needed). Open `/api/health`, then `/` → login with the first-boot
credentials from the logs → WhatsApp → scan QR.

## Local run
```bash
cp .env.example .env
# set DATABASE_URL to local postgres
go run ./cmd/server
# http://localhost:8080  /api/health
```

Seed admin is auto-created on boot (random password printed once when
`ADMIN_SEED_PASSWORD` is unset). Migrations auto-run from `migrations/`.

## WhatsApp notes (unofficial)
whatsmeow = QR-paired companion, not Cloud API. Small ban/re-login risk. Session file: `/data/whatsapp.db` — keep the volume. If QR expires: Admin → WhatsApp → Logout → rescan. `WHATSAPP_ENABLED=false` runs stub mode (logs instead of sending) for testing without a phone.

## Limits on Railway Free (0.5GB RAM / 0.5GB vol)
- Single binary keeps RAM ~40-80MB (`GOMAXPROCS=1 GOGC=20` set in Dockerfile).
- Images stored at `/data/images`, 5MB max each. ~500 cars max on 0.5GB — add Cloudinary/S3 when full (code path: `internal/images`).
- Daily backup: `pg_dump $DATABASE_URL > backup.sql` (add Railway cron later).

## API cheat sheet
- `POST /api/auth/login` → token; use `Authorization: Bearer`.
- `POST /api/users` (admin) — create sales/admin logins.
- `GET /api/dashboard`, `/api/leads` (sales: assigned only), `/api/vehicles`, `/api/test-drives`, `/api/followups`, `/api/bookings`, `/api/conversations?`, `/api/messages?conversation_id=`
- `POST /api/vehicles`, `PATCH /api/vehicles/{id}`, `POST /api/vehicles/{id}/images` (multipart `file`)
- `POST /api/leads/{id}/match`, `GET /api/leads/{id}/more?offset=&limit=`, `PATCH /api/leads/{id}/interest`, `PATCH /api/leads/{id}/assign` (admin)
- `POST /api/test-drives` (409 on slot clash), `POST /api/followups`, `POST /api/bookings` (409 on double-book), `PATCH /api/bookings/{id}` (`COMPLETED` → vehicle DELIVERED, lead CONVERTED)
- `GET/POST /api/negotiations`, `GET/POST /api/finance`, `GET /api/sell-requests`, `GET/POST /api/reviews`, `GET /api/outbox`
- `GET /api/whatsapp/status|qr`, `POST /api/whatsapp/send|logout|simulate`
- `GET /api/metrics` (uptime, goroutines, pool, queues), `PATCH /api/conversations?id=` (`bot_enabled` takeover, close)
 - Backups: `BACKUP_DIR=/data/backups ./scripts/backup.sh` (cron daily), restore drill: `./scripts/restore.sh <file>`
 - Full TC list + QA checklist: `TESTING.md`. No payment endpoints by design.

## Production runbook
- **One replica only.** The WhatsApp device + scheduler + outbox live in this process; two replicas flap the socket and double-send. Never scale past 1.
- **Deploy verify:** `GET /api/health` → check `version` equals the pushed short SHA, `db: true`, `whatsapp.status: connected`. Mismatched version = old build still serving.
- **Backups (pg_dump ships in the image):** Railway cron daily: `BACKUP_DIR=/data/backups ./scripts/backup.sh` with `DATABASE_URL` set. Keeps newest 7. Monthly: restore drill into a fresh DB (`./scripts/restore.sh`) and compare row counts.
- **WhatsApp flap:** `connecting` with rising `cycles_10m` = socket drops (redeploy overlap, phone offline, companion killed). Steady state is `connected`, `fail_count: 0`. `expired`/`logged_out` → admin Reconnect → rescan QR. Every Stopping/Starting Container pair briefly flaps — avoid rapid successive deploys.
- **Timeouts:** chats idle 30 min auto-close; bare `hi` always reopens the menu.
- **Limits:** 600 req/min/IP on `/api/*` (`/api/health`, `/api/metrics` exempt); login 10 fails/5 min → 429; uploads 5MB, sniffed jpg/png/webp, 10/vehicle.
- **Secrets:** `JWT_SECRET` auto-generates into `/data/jwt.secret` when unset
  (set it only to pin/rotate); seed creds print once at first boot when
  `ADMIN_SEED_PASSWORD` is unset — rotate after staff exit; last-admin guards block self-demote/delete.
- **Disk:** `/api/health` `disk_used_pct` — images at `/data/images`; ~500 cars max on 0.5GB, then move to S3/Cloudinary (`internal/images`).
