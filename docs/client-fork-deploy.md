# Client Guide — Fork, Connect & Deploy AutoCart on Railway

You need: a GitHub account and a Railway account. No coding, no terminal,
no environment variables to type.

## 1. Fork the code (1 click)

1. Open the AutoCart repository on GitHub.
2. Press **Fork** (top right) → create the fork under your own account.
3. This is now *your* copy. Updates to the original never touch it unless
   you choose to pull them in.

## 2. Connect Railway (2 clicks)

1. Railway dashboard → **New Project → Deploy from Repo** → pick your fork.
2. Railway builds the app automatically and starts the first deploy.

## 3. Add database + storage (once, 1 minute)

Plain repo deploys cannot create these by themselves — add them once:

1. **Add a service → Database → PostgreSQL.**
2. On the app service, add variable `DATABASE_URL` as a **reference** to
   `Postgres.DATABASE_URL` (reference, not a pasted value — it stays in
   sync automatically).
3. On the app service, attach a **Volume**: mount path `/data`, size 512MB.
4. Enable **Public Networking** on the app service (this gives you the app URL).

## 4. Environment variables — what happens automatically

You type **nothing**. Summary:

| Variable | How it gets set |
|---|---|
| `PORT` | Injected by Railway automatically |
| `DATA_DIR` | Baked into the Docker image (`/data`) |
| `WHATSAPP_ENABLED`, `TIMEZONE` | Built-in defaults (`true`, `Asia/Kuala_Lumpur`) |
| `JWT_SECRET` | Generated once by the app, saved into the `/data` volume |
| `ADMIN_SEED_PASSWORD` | Generated at first boot, printed **once** in deploy logs |
| `DATABASE_URL` | The one manual step (section 3, step 2) — automatic after that |

Changing any variable in the Railway dashboard triggers a redeploy by
itself, and values persist across restarts. Because secrets are generated
inside *your* deployment, forking never leaks anyone's credentials.

## 5. First login

1. App service → **Deployments** → latest → **View Logs**.
2. Find: `FIRST BOOT — admin created. Login: <email> / <password>`.
   Save it — it shows only once.
3. Open your app URL → log in.

## 6. Pair WhatsApp (1 minute)

1. In the admin panel open the **Guide** page → step 1 → **Open WhatsApp**.
2. Press **Refresh**. Scan the QR with your phone
   (WhatsApp → Settings → Linked devices → Link a device).
3. Wait for the status pill to turn **connected**.
4. No phone handy? Use the **Simulator** page — same bot engine, no pairing.

Lost the QR or stuck on "connecting"? Press **Reconnect** for a fresh code.
Full walkthrough lives in the in-app **Guide** page.

## 7. Going forward — auto-deploys

Every `git push` to your fork's `main` branch redeploys automatically.
Database migrations run on boot, and the `/data` volume keeps your photos,
WhatsApp session, and generated secret across deploys. Keep **one replica**
— the WhatsApp connection is single-process.

## Template link vs fork — which one?

- **Template link** (`railway.com/new/template/<CODE>`): fastest start,
  everything pre-wired (database reference, volume, domain). The service
  stays attached to the original repo, so you get prompted when upstream
  updates land. Want full independence later? Service → Settings →
  Source → **Eject** creates your own repo copy.
- **Fork first** (this guide): your own repo from day one, you control
  every update. Costs you the two manual adds in section 3.

## Troubleshooting

- `/api/health` shows `db: false` → Postgres isn't linked yet; check the
  `DATABASE_URL` reference (section 3, step 2).
- Deploy never turns healthy → Service → Settings → Healthcheck Path must
  be `/api/health` (not `/health`).
- QR expired / session dead → **Reconnect**, scan the fresh code.
- Lost the admin password → set `ADMIN_SEED_PASSWORD` in Railway variables
  and redeploy to pin a known one (admin only).
