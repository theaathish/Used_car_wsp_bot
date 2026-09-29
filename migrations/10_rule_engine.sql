-- bot_flows: groups of questions (e.g. "Welcome", "Vehicle Enquiry")
CREATE TABLE IF NOT EXISTS bot_flows (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL DEFAULT '',
  slug TEXT NOT NULL DEFAULT '',
  is_entry_flow BOOLEAN NOT NULL DEFAULT FALSE,
  trigger_matching BOOLEAN NOT NULL DEFAULT FALSE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_bot_flow_slug ON bot_flows(slug);

-- bot_questions: each step in a flow
CREATE TABLE IF NOT EXISTS bot_questions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  flow_id UUID NOT NULL REFERENCES bot_flows(id) ON DELETE CASCADE,
  field_name TEXT NOT NULL DEFAULT '',
  question_text TEXT NOT NULL DEFAULT '',
  question_type TEXT NOT NULL DEFAULT 'text',
  validation_rule TEXT NOT NULL DEFAULT '',
  allowed_values JSONB NOT NULL DEFAULT '[]',
  error_message TEXT NOT NULL DEFAULT 'Invalid input. Please try again.',
  next_question_id UUID REFERENCES bot_questions(id) ON DELETE SET NULL,
  is_required BOOLEAN NOT NULL DEFAULT TRUE,
  order_index INT NOT NULL DEFAULT 0,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_bq_flow ON bot_questions(flow_id, order_index);

-- bot_conditions: conditional routing after a question
CREATE TABLE IF NOT EXISTS bot_conditions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  question_id UUID NOT NULL REFERENCES bot_questions(id) ON DELETE CASCADE,
  field_name TEXT NOT NULL DEFAULT '',
  operator TEXT NOT NULL DEFAULT 'EQUALS',
  value TEXT NOT NULL DEFAULT '',
  target_question_id UUID REFERENCES bot_questions(id) ON DELETE SET NULL,
  target_flow_id UUID REFERENCES bot_flows(id) ON DELETE SET NULL,
  priority INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_bc_question ON bot_conditions(question_id, priority);

-- bot_responses: admin-configurable response templates
CREATE TABLE IF NOT EXISTS bot_responses (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  response_key TEXT UNIQUE NOT NULL,
  response_text TEXT NOT NULL DEFAULT '',
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- conversation_answers: audit trail of every valid answer
CREATE TABLE IF NOT EXISTS conversation_answers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  question_id UUID NOT NULL REFERENCES bot_questions(id) ON DELETE CASCADE,
  field_name TEXT NOT NULL DEFAULT '',
  value_captured TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ca_conv ON conversation_answers(conversation_id, created_at);

-- Add state tracking to conversations
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS current_flow_id UUID REFERENCES bot_flows(id) ON DELETE SET NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS current_question_id UUID REFERENCES bot_questions(id) ON DELETE SET NULL;

-- Add JSONB extracted_data to leads for fast vehicle matching
ALTER TABLE leads ADD COLUMN IF NOT EXISTS extracted_data JSONB NOT NULL DEFAULT '{}';

-- Seed default bot_responses for all admin-configurable response types
INSERT INTO bot_responses(response_key, response_text) VALUES
  ('welcome', 'Welcome! How can we help you today?'),
  ('invalid_input', 'Sorry, I didn''t understand that. Please try again.'),
  ('no_matching_vehicle', 'Sorry, we don''t have any vehicles matching your criteria right now. Our team will follow up if new stock arrives.'),
  ('flow_complete', 'Thank you! Our team will follow up with you shortly.'),
  ('session_expired', 'Your session has expired. Let''s start fresh!'),
  ('bot_disabled', 'A team member will respond to you shortly.'),
  ('greeting', 'Hello! Welcome back.')
ON CONFLICT (response_key) DO NOTHING;
