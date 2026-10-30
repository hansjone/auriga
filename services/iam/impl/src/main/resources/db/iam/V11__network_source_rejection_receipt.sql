-- A rejected Register has no source; retain its independently verified original ownership anchor.
ALTER TABLE iam.network_source_operation
  ALTER COLUMN source_id DROP NOT NULL,
  ADD COLUMN anchor_id uuid,
  ADD FOREIGN KEY (tenant_id,anchor_id) REFERENCES iam.network_source_anchor(tenant_id,id),
  ADD CHECK ((source_id IS NOT NULL AND anchor_id IS NULL)
    OR (method='Register' AND source_id IS NULL AND anchor_id IS NOT NULL));
