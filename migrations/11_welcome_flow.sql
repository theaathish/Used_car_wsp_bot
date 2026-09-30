-- 11_welcome_flow.sql: Ensure Welcome & Menu flow is the active entry flow and reset buy/sell branching
INSERT INTO bot_flows(id, name, slug, is_entry_flow, trigger_matching, is_active)
VALUES ('11111111-1111-1111-1111-111111111100', 'Welcome & Menu', 'welcome_flow', true, false, true)
ON CONFLICT (slug) DO UPDATE
SET is_entry_flow = true, trigger_matching = false, is_active = true, updated_at = now();

-- Unset is_entry_flow on all other flows so Welcome & Menu is the only entry flow
UPDATE bot_flows SET is_entry_flow = false WHERE slug != 'welcome_flow';
UPDATE bot_flows SET is_entry_flow = true WHERE slug = 'welcome_flow';

-- Upsert Welcome & Menu question
INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, is_required, order_index, is_active)
VALUES ('22222222-2222-2222-2222-222222222001', '11111111-1111-1111-1111-111111111100', 'service_intent', '🚗 *Welcome to AutoKart!*\n\nHow can we help you today?\n\n1️⃣ *Buy a Car* — Browse our verified pre-owned cars\n2️⃣ *Sell Your Car* — Instant evaluation & listing\n\n👉 Reply *1* or *BUY* to browse cars\n👉 Reply *2* or *SELL* to sell your car', 'select', '', '["Buy","Sell","1","2"]'::jsonb, 'Please reply *1* (or BUY) to browse cars, or *2* (or SELL) to sell your car.', true, 1, true)
ON CONFLICT (id) DO UPDATE
SET field_name = 'service_intent',
    question_text = EXCLUDED.question_text,
    question_type = 'select',
    allowed_values = EXCLUDED.allowed_values,
    error_message = EXCLUDED.error_message,
    is_required = true,
    order_index = 1,
    is_active = true,
    updated_at = now();

-- Upsert Welcome conditions to route to Buy or Sell flows
INSERT INTO bot_conditions(id, question_id, field_name, operator, value, target_flow_id, priority) VALUES
  ('33333333-3333-3333-3333-333333333001', '22222222-2222-2222-2222-222222222001', 'service_intent', 'eq', 'Buy', '11111111-1111-1111-1111-111111111101', 1),
  ('33333333-3333-3333-3333-333333333002', '22222222-2222-2222-2222-222222222001', 'service_intent', 'eq', '1', '11111111-1111-1111-1111-111111111101', 2),
  ('33333333-3333-3333-3333-333333333003', '22222222-2222-2222-2222-222222222001', 'service_intent', 'eq', 'Sell', '11111111-1111-1111-1111-111111111102', 3),
  ('33333333-3333-3333-3333-333333333004', '22222222-2222-2222-2222-222222222001', 'service_intent', 'eq', '2', '11111111-1111-1111-1111-111111111102', 4)
ON CONFLICT (id) DO UPDATE
SET operator = 'eq', value = EXCLUDED.value, target_flow_id = EXCLUDED.target_flow_id, priority = EXCLUDED.priority;

-- Reset open conversations that were stuck on old buy_flow initial question
UPDATE conversations
SET current_flow_id = '11111111-1111-1111-1111-111111111100',
    current_question_id = '22222222-2222-2222-2222-222222222001'
WHERE current_flow_id = '11111111-1111-1111-1111-111111111101'
  AND current_question_id = '22222222-2222-2222-2222-222222222101';
