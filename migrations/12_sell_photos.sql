-- 12_sell_photos.sql: Add vehicle photo collection question (2 photos) to sell_flow

-- Update Question 8 (sell_location) to point to Question 9 (sell_photos)
UPDATE bot_questions
SET next_question_id = '22222222-2222-2222-2222-222222222209', updated_at = now()
WHERE id = '22222222-2222-2222-2222-222222222208';

-- Insert Question 9: sell_photos
INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, next_question_id, is_required, order_index, is_active)
VALUES (
  '22222222-2222-2222-2222-222222222209',
  '11111111-1111-1111-1111-111111111102',
  'sell_photos',
  '📸 Please upload 2 photos of your car (e.g. exterior front & rear, or interior dashboard) for valuation.\n\nYou can upload the photos now, or reply *SKIP* to proceed without photos.',
  'photo',
  '',
  '[]'::jsonb,
  'Please upload a photo of your car or reply *SKIP*.',
  NULL,
  false,
  9,
  true
)
ON CONFLICT (id) DO UPDATE
SET question_text = EXCLUDED.question_text,
    question_type = EXCLUDED.question_type,
    validation_rule = EXCLUDED.validation_rule,
    allowed_values = EXCLUDED.allowed_values,
    error_message = EXCLUDED.error_message,
    next_question_id = NULL,
    is_required = false,
    order_index = 9,
    is_active = true,
    updated_at = now();
