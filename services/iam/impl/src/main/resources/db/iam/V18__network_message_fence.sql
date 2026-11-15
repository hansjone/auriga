-- Message quarantine has its own lifetime, independent of device/session revocation.
CREATE TABLE iam.network_message_fence (
  tenant_id uuid NOT NULL,
  device_id uuid NOT NULL,
  target_id text NOT NULL,
  control_revision numeric(20,0) NOT NULL CHECK (control_revision > 0),
  control_digest text NOT NULL,
  PRIMARY KEY (tenant_id, device_id, target_id)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_message_fence');
