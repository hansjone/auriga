-- UAC authority is shared by all IAM replicas and is subject to the runtime tenant RLS role.
CREATE TABLE iam.uac_challenge (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz,
 UNIQUE(tenant_id,lookup_key), UNIQUE(tenant_id,id),
 provider_id uuid GENERATED ALWAYS AS ((data->>'providerId')::uuid) STORED NOT NULL,
 FOREIGN KEY(tenant_id,provider_id) REFERENCES iam.idp_provider(tenant_id,id)
);
CREATE TABLE iam.uac_credential (LIKE iam.uac_challenge INCLUDING ALL);
ALTER TABLE iam.uac_credential ADD CONSTRAINT uac_credential_provider_fk
 FOREIGN KEY(tenant_id,provider_id) REFERENCES iam.idp_provider(tenant_id,id);
CREATE TABLE iam.uac_job (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz,
 next_at timestamptz NOT NULL DEFAULT now(), lease_until timestamptz,
 UNIQUE(tenant_id,lookup_key), UNIQUE(tenant_id,id)
);
CREATE INDEX uac_job_due ON iam.uac_job(tenant_id,next_at,lease_until);
CREATE TABLE iam.uac_navigation (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz,
 UNIQUE(tenant_id,lookup_key), UNIQUE(tenant_id,id)
);
CREATE TABLE iam.uac_suppression (LIKE iam.uac_navigation INCLUDING ALL);
CREATE TABLE iam.uac_diagnostics (LIKE iam.uac_navigation INCLUDING ALL);
SELECT iam.auriga_enable_tenant_rls('iam.uac_challenge');
SELECT iam.auriga_enable_tenant_rls('iam.uac_credential');
SELECT iam.auriga_enable_tenant_rls('iam.uac_job');
SELECT iam.auriga_enable_tenant_rls('iam.uac_navigation');
SELECT iam.auriga_enable_tenant_rls('iam.uac_suppression');
SELECT iam.auriga_enable_tenant_rls('iam.uac_diagnostics');
CREATE FUNCTION iam.uac_job_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.next_at := (NEW.data->>'nextAt')::timestamptz;
 NEW.lease_until := (NEW.data->>'leaseUntil')::timestamptz;
 RETURN NEW;
END $$;
CREATE TRIGGER uac_job_schedule BEFORE INSERT OR UPDATE ON iam.uac_job
 FOR EACH ROW EXECUTE FUNCTION iam.uac_job_schedule();
-- Tenant directory contains no user data. Workers enter TenantContext before touching authority.
CREATE TABLE iam.uac_work_tenant(tenant_id uuid PRIMARY KEY);
