-- Idempotency records outlive their short, exclusive thread reservation.
ALTER TABLE network_submit
  ADD COLUMN reservation_until timestamptz,
  ADD COLUMN failure_code text;

UPDATE network_submit
SET reservation_until = least(created_at + interval '30 seconds',
    (message_json::jsonb->>'expires_at')::timestamptz)
WHERE result_json IS NULL;

ALTER TABLE network_submit
  ADD CONSTRAINT network_submit_terminal_exclusive CHECK (
    (failure_code IS NULL OR (failure_code = 'network.conflict'
      AND result_json IS NULL AND reservation_until IS NULL))
    AND (result_json IS NULL OR reservation_until IS NULL));

CREATE INDEX network_submit_active_thread_idx
  ON network_submit (tenant_id, ((message_json::jsonb)->>'thread_id'))
  WHERE reservation_until IS NOT NULL;
