-- Maintenance metadata supplements the shared DSH authority tables.
ALTER TABLE instance ADD COLUMN shared_rebuild_hold boolean NOT NULL DEFAULT false;
ALTER TABLE shared_dsh_maintenance ADD COLUMN batch_id uuid;
ALTER TABLE shared_dsh_maintenance ADD COLUMN legacy_instance_id uuid;
ALTER TABLE shared_dsh_maintenance ADD COLUMN export_until bigint;
ALTER TABLE shared_dsh_maintenance ADD COLUMN planned_by uuid;
ALTER TABLE shared_dsh_maintenance ADD COLUMN planned_at bigint;
ALTER TABLE shared_dsh_maintenance ADD CONSTRAINT shared_maintenance_legacy_fk
 FOREIGN KEY(tenant_id,legacy_instance_id) REFERENCES instance(tenant_id,id);
CREATE INDEX shared_maintenance_batch ON shared_dsh_maintenance(tenant_id,batch_id);
ALTER TABLE shared_dsh_operation ADD COLUMN purpose text NOT NULL DEFAULT 'switch'
 CHECK(purpose IN ('switch','maintenance'));
