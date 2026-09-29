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

-- Seed default Buy and Sell flows
INSERT INTO bot_flows(id, name, slug, is_entry_flow, trigger_matching, is_active) VALUES
  ('11111111-1111-1111-1111-111111111101', 'Buy a Car', 'buy_flow', true, true, true),
  ('11111111-1111-1111-1111-111111111102', 'Sell a Car', 'sell_flow', false, false, true)
ON CONFLICT (slug) DO NOTHING;

-- Seed questions for Buy flow
INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, is_required, order_index) VALUES
  ('22222222-2222-2222-2222-222222222101', '11111111-1111-1111-1111-111111111101', 'vehicle_type', 'What type of car are you looking for? (SUV, Sedan, Hatchback, MPV, Any)', 'select', '', '["SUV","Sedan","Hatchback","MPV","Any"]'::jsonb, 'Please choose one of: SUV, Sedan, Hatchback, MPV, or Any.', true, 1),
  ('22222222-2222-2222-2222-222222222102', '11111111-1111-1111-1111-111111111101', 'budget_max', 'What is your maximum budget in RM? (e.g. 50000)', 'number', 'min:0,max:99999999', '[]'::jsonb, 'Please enter a valid budget amount.', true, 2),
  ('22222222-2222-2222-2222-222222222103', '11111111-1111-1111-1111-111111111101', 'brand', 'Which brand/make do you prefer? (e.g. Toyota, Honda, BMW, or Any)', 'text', '', '[]'::jsonb, 'Please specify a preferred brand or Any.', true, 3),
  ('22222222-2222-2222-2222-222222222104', '11111111-1111-1111-1111-111111111101', 'model', 'Any specific model? (e.g. Civic, Vios, or Any)', 'text', '', '[]'::jsonb, 'Please specify a model or Any.', true, 4),
  ('22222222-2222-2222-2222-222222222105', '11111111-1111-1111-1111-111111111101', 'fuel', 'What fuel type do you prefer? (Petrol, Diesel, CNG, Electric, Hybrid, Any)', 'select', '', '["Petrol","Diesel","CNG","Electric","Hybrid","Any"]'::jsonb, 'Please choose one of: Petrol, Diesel, CNG, Electric, Hybrid, or Any.', true, 5),
  ('22222222-2222-2222-2222-222222222106', '11111111-1111-1111-1111-111111111101', 'transmission', 'Transmission preference? (Manual, Automatic, Any)', 'select', '', '["Manual","Automatic","Any"]'::jsonb, 'Please choose Manual, Automatic, or Any.', true, 6),
  ('22222222-2222-2222-2222-222222222107', '11111111-1111-1111-1111-111111111101', 'year_min', 'Minimum manufacturing year? (e.g. 2018 or Any)', 'number', 'min:1995,max:2027', '[]'::jsonb, 'Please enter a valid year between 1995 and 2027.', false, 7)
ON CONFLICT (id) DO NOTHING;

-- Seed condition on vehicle_type = 'Any' -> skip to brand question
INSERT INTO bot_conditions(id, question_id, field_name, operator, value, target_question_id, priority) VALUES
  ('33333333-3333-3333-3333-333333333101', '22222222-2222-2222-2222-222222222101', 'vehicle_type', 'eq', 'Any', '22222222-2222-2222-2222-222222222103', 1)
ON CONFLICT (id) DO NOTHING;

-- Seed questions for Sell flow
INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, is_required, order_index) VALUES
  ('22222222-2222-2222-2222-222222222201', '11111111-1111-1111-1111-111111111102', 'sell_brand', 'What brand/make is your car? (e.g. Toyota, Honda, Proton)', 'text', '', '[]'::jsonb, 'Please enter the car brand.', true, 1),
  ('22222222-2222-2222-2222-222222222202', '11111111-1111-1111-1111-111111111102', 'sell_model', 'What is the car model? (e.g. Myvi, City, Vios)', 'text', '', '[]'::jsonb, 'Please enter the car model.', true, 2),
  ('22222222-2222-2222-2222-222222222203', '11111111-1111-1111-1111-111111111102', 'sell_year', 'Which year was it manufactured? (e.g. 2019)', 'number', 'min:1990,max:2027', '[]'::jsonb, 'Please enter a valid year.', true, 3),
  ('22222222-2222-2222-2222-222222222204', '11111111-1111-1111-1111-111111111102', 'sell_km', 'What is the current mileage in kilometers? (e.g. 45000)', 'number', 'min:0,max:999999', '[]'::jsonb, 'Please enter the mileage in kilometers.', true, 4),
  ('22222222-2222-2222-2222-222222222205', '11111111-1111-1111-1111-111111111102', 'sell_fuel', 'What fuel type does it use? (Petrol, Diesel, Hybrid, Electric)', 'select', '', '["Petrol","Diesel","Hybrid","Electric"]'::jsonb, 'Please select: Petrol, Diesel, Hybrid, or Electric.', true, 5),
  ('22222222-2222-2222-2222-222222222206', '11111111-1111-1111-1111-111111111102', 'sell_transmission', 'What transmission is it? (Automatic, Manual)', 'select', '', '["Automatic","Manual"]'::jsonb, 'Please select Automatic or Manual.', true, 6),
  ('22222222-2222-2222-2222-222222222207', '11111111-1111-1111-1111-111111111102', 'sell_condition', 'What is the overall condition? (Excellent, Good, Fair, Poor)', 'select', '', '["Excellent","Good","Fair","Poor"]'::jsonb, 'Please select: Excellent, Good, Fair, or Poor.', true, 7),
  ('22222222-2222-2222-2222-222222222208', '11111111-1111-1111-1111-111111111102', 'sell_location', 'Where is the car located? (e.g. Kuala Lumpur, Petaling Jaya)', 'text', '', '[]'::jsonb, 'Please enter the location of the car.', true, 8)
ON CONFLICT (id) DO NOTHING;
