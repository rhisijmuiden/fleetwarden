-- Phase 17 load test (2,000 agents): many queries look devices up by their id
-- as text (the id from the URL or the agent). Index that expression, and leave
-- room on each page so the frequent heartbeat updates stay HOT updates.
CREATE INDEX IF NOT EXISTS devices_id_text ON devices ((id::text));
ALTER TABLE devices SET (fillfactor = 80);
