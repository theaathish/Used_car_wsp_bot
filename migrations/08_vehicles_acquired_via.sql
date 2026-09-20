-- V8: add acquired_via column to vehicles to track source (purchase, exchange, etc.)
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS acquired_via TEXT;
COMMENT ON COLUMN vehicles.acquired_via IS 'How the vehicle was acquired: purchase, exchange, etc.';
