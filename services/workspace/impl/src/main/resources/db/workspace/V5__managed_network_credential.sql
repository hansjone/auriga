ALTER TABLE workspace.instance
  ADD COLUMN managed_credential_version bigint NOT NULL DEFAULT 1 CHECK (managed_credential_version BETWEEN 1 AND 9007199254740991),
  ADD COLUMN managed_credential bytea,
  ADD COLUMN managed_synced_version bigint NOT NULL DEFAULT 0,
  ADD COLUMN managed_retry_at timestamptz NOT NULL DEFAULT now();
