import { defineRailway, postgres, project, service, volume } from "railway/iac";

// SellingBot V1 — Railway project as code (IaC, generally available).
//
// This is the template definition: one app service + managed Postgres +
// persistent volume, wired with zero manually-set secrets.
//
// Zero-config boot (see internal/config/config.go):
//   - JWT_SECRET unset  -> generated once, persisted to DATA_DIR/jwt.secret
//     (set JWT_SECRET to pin/rotate it manually).
//   - ADMIN_SEED_PASSWORD unset -> random password generated at first boot
//     and printed once in the deploy logs (pin via env to fix it).
// So this file deliberately sets NO secrets — only wiring + safe defaults.
//
// Usage:
//   npm install railway          # once, so `railway config plan` can evaluate this
//   railway link                 # connect to your Railway project/environment
//   railway config plan          # preview (secrets redacted by default)
//   railway config apply         # confirm + apply
//
// Migration note: this service previously used railway.json / railway.toml
// (Config as Code, deprecated, hard cutoff 2026-12-01). A service cannot be
// managed by both systems. Before the first `railway config apply`, remove
// railway.json + railway.toml from the repo (or run
// `railway config migrate --apply --delete-files`), then plan/apply.
// The Dockerfile at the repo root is auto-detected for the build, and the
// image CMD ("/app/server") is the start command — neither needs declaring here.

export default defineRailway(() => {
  const db = postgres("postgres");

  // 0.5 GB volume — matches Railway free-tier allowance. Images live at
  // /data/images, WhatsApp session at /data/whatsapp.db, JWT secret at
  // /data/jwt.secret. Keep the mount path /data (app DATA_DIR).
  const data = volume("sellingbot-data", {
    sizeMB: 512,
  });

  const app = service("sellingbot", {
    start: "/app/server",
    healthcheck: "/api/health",
    healthcheckTimeout: 60,
    // One replica only. The WhatsApp device + follow-up scheduler + outbox
    // live in this process; two replicas flap the socket and double-send.
    replicas: 1,
    volumeMounts: {
      "/data": data,
    },
    env: {
      DATABASE_URL: db.env.DATABASE_URL,
      DATA_DIR: "/data",
      WHATSAPP_ENABLED: "true",
      TIMEZONE: "Asia/Kuala_Lumpur",
      // JWT_SECRET, ADMIN_SEED_EMAIL, ADMIN_SEED_PASSWORD intentionally
      // unset — see zero-config boot above.
    },
  });

  return project("sellingbot", {
    resources: [app, db, data],
  });
});
