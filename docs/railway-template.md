# Railway Template Configuration

`railway.json` only covers a single service (build/deploy) — it cannot
provision Postgres, volumes, or cross-service wiring. Railway templates are a
separate format with no published fetchable schema, so this file is the source
of truth when creating or updating the SellingBot template in the Template
Composer. `.railway/railway.ts` mirrors the same topology as applyable IaC.

## Services

### `sellingbot` (from this repo)

- Source: this GitHub repo (public), root directory, `main` branch
- Builder: Dockerfile (`Dockerfile` at repo root)
- Start command: `/app/server` (image CMD; leave empty also works)
- Healthcheck path: `/api/health`, timeout 60
- Restart policy: ON_FAILURE
- Replicas: **1** — the WhatsApp device + scheduler + outbox live in this
  process; two replicas flap the socket and double-send. Never scale past 1.
- Public networking: HTTP enabled (Railway assigns the domain)
- Variables (wiring + safe defaults only — **no secrets**):
  - `DATABASE_URL` = reference → `postgres.DATABASE_URL`
  - `DATA_DIR` = `/data`
  - `WHATSAPP_ENABLED` = `true`
  - `TIMEZONE` = `Asia/Kuala_Lumpur`
  - `JWT_SECRET`, `ADMIN_SEED_EMAIL`, `ADMIN_SEED_PASSWORD`: leave **unset**.
    Zero-config boot handles them: JWT secret is generated once and persisted
    to `/data/jwt.secret`; the admin password is generated at first boot and
    printed once in the deploy logs. Set them only to pin/rotate.

### `postgres` (managed database)

- Source: Railway Postgres template/service (`postgres` helper in IaC)
- No custom config; the app's migrations auto-run from `migrations/` on boot
  and retry the DB connection (~up to a few minutes) while Postgres starts.

## Volume

Attach one Railway volume to the `sellingbot` service:

- Mount path: `/data`
- Size: 512 MB (free-tier allowance)
- Purpose: car images (`/data/images`), WhatsApp session
  (`/data/whatsapp.db`), generated JWT secret (`/data/jwt.secret`), backups
  (`/data/backups`). Ephemeral filesystem does not persist — without this
  mount, sessions/images/secrets are lost on every redeploy.

When deployed from the published template, Railway provisions this volume
automatically. Direct repo deploys must still attach it manually.

## Publish / update flow

1. Push this repo to GitHub (must be public for a marketplace template).
2. Railway → Templates → New Template (or Settings → Generate Template from
   a working project that already has the above topology).
3. Add the `sellingbot` service from this repo + a Postgres database.
4. Wire variables and attach the `/data` volume exactly as above.
5. Enable public networking on `sellingbot`.
6. Create/Publish the template, copy the template code from its URL
   (`https://railway.com/new/template/<CODE>`).
7. Paste `<CODE>` into the Deploy button in `README.md` (replacing
   `SELLINGBOT_TEMPLATE_CODE`).

## Validation (fresh project from the template)

1. Project contains `sellingbot` + `postgres` services.
2. `sellingbot` has one attached volume mounted at `/data`.
3. `DATABASE_URL` resolves (reference variable, not a literal).
4. Deploy logs show `sellingbot starting port=... datadir=/data` with no
   required-var fatals, then `FIRST BOOT — admin created. Login: ...`.
5. `GET /api/health` returns `db: true`.
6. Redeploy preserves `/data/whatsapp.db`, `/data/images`, `/data/jwt.secret`
   (sessions stay valid across restarts).
