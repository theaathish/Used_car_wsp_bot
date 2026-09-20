# Railway IaC — `.railway/railway.ts`

Project-as-code for SellingBot (app + Postgres + volume). Evaluated by the
Railway CLI, not during deploys.

```bash
npm install railway
railway link
railway config plan     # safe preview, secrets redacted
railway config apply    # confirm + apply
```

Notes:
- First apply migrates off `railway.json` / `railway.toml` (deprecated,
  cutoff 2026-12-01). Delete those files before applying (or
  `railway config migrate --apply --delete-files`).
- No secrets live in this file. `JWT_SECRET` auto-generates into
  `/data/jwt.secret`; the admin password auto-generates and prints once in
  the first-boot logs. Set them as Railway variables only to pin/rotate.
- Keep replicas at 1 (WhatsApp socket + scheduler are single-process).
