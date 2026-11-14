-- Preserve producer deduplication for the whole original business TTL and the receipt horizon.
UPDATE network_submit SET retain_until = GREATEST(retain_until,
    (message_json::jsonb ->> 'expires_at')::timestamptz + interval '7 days')
WHERE message_json::jsonb ->> 'expires_at' IS NOT NULL;
