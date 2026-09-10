# SellingBot V1 — single-service Railway deploy

Go monolith: REST API + WhatsApp worker (whatsmeow) + 60s follow-up scheduler + embedded admin UI. One service + Railway Postgres + 0.5GB volume.

## 1-click Railway deploy
1. Push this folder to GitHub.
2. Railway → New Project → Deploy from Repo.
3. Add Plugin → PostgreSQL (gives `DATABASE_URL`).
4. Add Volume → mount path `/data`.
5. Variables:
   - `JWT_SECRET` = 32 random chars
   - `ADMIN_SEED_EMAIL`, `ADMIN_SEED_PASSWORD`
   - `WHATSAPP_ENABLED=true`, `DATA_DIR=/data`
6. Deploy. Open `/api/health`, then `/` → login → WhatsApp → scan QR.

## Local run
```bash
cp .env.example .env
# set DATABASE_URL to local postgres
go run ./cmd/server
# http://localhost:8080  /api/health
```

Seed admin is auto-created from env on boot. Migrations auto-run from `migrations/`.

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
