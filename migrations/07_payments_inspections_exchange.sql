-- V7: payments (manual log), inspections (sell flow), exchange valuations.
-- All idempotent for Railway redeploys.

-- Manual payment log per booking. No auto transitions; staff data entry only.
CREATE TABLE IF NOT EXISTS payments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  amount INT NOT NULL DEFAULT 0,
  method TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'PENDING',
  recorded_by TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_payments_booking ON payments(booking_id);

-- Sell inspection appointments. Created BEFORE sell_requests valuation row.
CREATE TABLE IF NOT EXISTS inspections (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  lead_id UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
  scheduled_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'SCHEDULED',
  notes TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_inspections_scheduled ON inspections(scheduled_at);
-- Same-lead double-book guard (mirrors uq_td_slot for test drives).
CREATE UNIQUE INDEX IF NOT EXISTS uq_inspection_slot
  ON inspections(lead_id, scheduled_at)
  WHERE status IN ('SCHEDULED');

-- Exchange trade-in valuation queue. Mirrors sell_requests accept/reject/reopen.
CREATE TABLE IF NOT EXISTS exchange_valuations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  lead_id UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
  current_car TEXT NOT NULL DEFAULT '',
  want TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'VALUATION_PENDING',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_exchange_lead ON exchange_valuations(lead_id);
