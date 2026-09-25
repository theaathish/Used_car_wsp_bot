-- Single-owner election for the WhatsApp socket (fixes connect/drop flap).
-- The device session lives in shared Postgres storage, so two processes
-- (redeploy overlap, scaled replicas, local dev against prod DB) calling
-- Connect on the same device kick each other off in a tight 30s loop.
-- Only the leader row owner may Connect; losers stay standby and never
-- touch the socket.
CREATE TABLE IF NOT EXISTS whatsapp_leader (
  id TEXT PRIMARY KEY,
  owner TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO whatsapp_leader(id, owner) VALUES ('leader', '')
ON CONFLICT (id) DO NOTHING;
