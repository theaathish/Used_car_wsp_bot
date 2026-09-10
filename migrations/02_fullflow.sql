-- V1 full-flow upgrade: sources, sales, negotiations, finance, reviews,
-- outbox, WhatsApp dedup, booking/test-drive concurrency guards.

-- Lead source + salesperson assignment
ALTER TABLE leads ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'whatsapp';
ALTER TABLE leads ADD COLUMN IF NOT EXISTS sales_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE leads ADD COLUMN IF NOT EXISTS interest TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_leads_sales ON leads(sales_user_id);

-- Requirements: model step was missing in V1
ALTER TABLE requirements ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '';

-- Sell intake structured fields (also mirrored in leads.state_data)
CREATE TABLE IF NOT EXISTS sell_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  lead_id UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
  brand TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  year INT NOT NULL DEFAULT 0,
  registration TEXT NOT NULL DEFAULT '',
  km INT NOT NULL DEFAULT 0,
  fuel TEXT NOT NULL DEFAULT '',
  transmission TEXT NOT NULL DEFAULT '',
  condition TEXT NOT NULL DEFAULT '',
  location TEXT NOT NULL DEFAULT '',
  photo_count INT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'VALUATION_PENDING',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Finance = informational only, no gateway
CREATE TABLE IF NOT EXISTS finance_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  lead_id UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
  loan_amount INT NOT NULL DEFAULT 0,
  tenure_months INT NOT NULL DEFAULT 0,
  employment TEXT NOT NULL DEFAULT '',
  income INT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'NEW',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Negotiation ledger
CREATE TABLE IF NOT EXISTS negotiations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  lead_id UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
  vehicle_id UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
  original_price INT NOT NULL DEFAULT 0,
  customer_offer INT NOT NULL DEFAULT 0,
  sales_offer INT NOT NULL DEFAULT 0,
  final_price INT NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Review + referral after delivery
CREATE TABLE IF NOT EXISTS reviews (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  rating INT NOT NULL DEFAULT 0,
  review TEXT NOT NULL DEFAULT '',
  referral_source TEXT NOT NULL DEFAULT '',
  referred_customer TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- WhatsApp dedup: one row per provider message id
CREATE TABLE IF NOT EXISTS processed_messages (
  wa_msg_id TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Outbox PENDING -> SENT for WhatsApp-unavailable windows
CREATE TABLE IF NOT EXISTS outbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  phone TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'PENDING',
  attempts INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox(status, created_at);

-- Test-drive slot guard: no double booking same vehicle+slot
CREATE UNIQUE INDEX IF NOT EXISTS uq_td_slot
  ON test_drives(vehicle_id, scheduled_at)
  WHERE status IN ('SCHEDULED');

-- Booking guard: only one active booking per vehicle
CREATE UNIQUE INDEX IF NOT EXISTS uq_booking_active_vehicle
  ON bookings(vehicle_id)
  WHERE status IN ('PENDING','CONFIRMED');
