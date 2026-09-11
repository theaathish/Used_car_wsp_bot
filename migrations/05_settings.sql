-- V5: business settings (timezone selectable from admin).

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO settings(key, value) VALUES ('timezone', 'Asia/Kolkata')
ON CONFLICT (key) DO NOTHING;
