ALTER TABLE ai_messages
  ADD COLUMN tool_call_id text,
  ADD COLUMN tool_calls jsonb;
