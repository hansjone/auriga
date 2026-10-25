-- Immutable canonical message and source attestation; transient audience signatures are minted
-- from these committed rows at FETCH, never persisted as reusable authorization.
ALTER TABLE network_message ADD COLUMN message_json text;
ALTER TABLE network_message ADD COLUMN source_context_json text;
ALTER TABLE network_message ADD COLUMN dispatch_context_json text;
-- Existing unstamped rows stay unreadable by the signed network path; do not fabricate identity.
ALTER TABLE network_message ADD CONSTRAINT network_source_stamp_pair
  CHECK ((message_json IS NULL) = (source_context_json IS NULL));
CREATE INDEX network_message_thread ON network_message (tenant_id, thread_id);
