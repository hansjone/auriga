ALTER TABLE workspace.instance
  ADD COLUMN managed_network_state text NOT NULL DEFAULT 'pending'
    CHECK (managed_network_state IN ('pending','ready','deferred','disabled','stopped')),
  ADD COLUMN managed_network_reason text,
  ADD COLUMN managed_wait_until timestamptz NOT NULL DEFAULT (now() + interval '30 seconds'),
  ADD COLUMN managed_restart_required boolean NOT NULL DEFAULT false;
UPDATE workspace.instance SET managed_network_state='ready'
  WHERE managed_credential IS NOT NULL AND managed_synced_version=managed_credential_version;