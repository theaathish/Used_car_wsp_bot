-- V2 deep-QA: takeover flag, delivery/message lifecycle, image dedup,
-- audit trail, idempotent reminder guards.

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS bot_enabled BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'SENT';

ALTER TABLE vehicle_images ADD COLUMN IF NOT EXISTS hash TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS audit_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  actor TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  entity TEXT NOT NULL DEFAULT '',
  entity_id TEXT NOT NULL DEFAULT '',
  old_value TEXT NOT NULL DEFAULT '',
  new_value TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_logs(entity, entity_id, created_at);

-- Exactly one reminder of each kind per test drive (REM-003 / §17).
CREATE UNIQUE INDEX IF NOT EXISTS uq_td_reminder
  ON followups(type, message)
  WHERE type LIKE 'testdrive\_reminder%';
