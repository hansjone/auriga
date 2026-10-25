-- Preserve legacy evidence as NULL; never infer a category from a live host or a retry.
ALTER TABLE network_local_registration
  ADD COLUMN address_type text CHECK (address_type IN ('session', 'subagent'));
