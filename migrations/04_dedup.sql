-- V3: deterministic duplicates are rejected by the database, not by luck.
-- Same logical follow-up twice -> second insert conflicts (409 / skip).
-- Same pending outbox delivery twice -> one row.

CREATE UNIQUE INDEX IF NOT EXISTS uq_followup_dedup ON followups
  (COALESCE(lead_id::text,''), COALESCE(customer_id::text,''), type, scheduled_at, message)
  WHERE status IN ('pending','sending');

CREATE UNIQUE INDEX IF NOT EXISTS uq_outbox_pending ON outbox (phone, body)
  WHERE status = 'PENDING';
