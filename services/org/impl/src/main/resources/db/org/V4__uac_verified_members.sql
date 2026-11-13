-- Deployment-provisioned verification records, owned and read only by org.
CREATE TABLE org.uac_employee_binding (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  provider_id uuid NOT NULL,
  employee text NOT NULL CHECK (length(employee) BETWEEN 1 AND 128),
  account_id uuid NOT NULL,
  verified_by uuid NOT NULL,
  verified_at timestamptz NOT NULL,
  UNIQUE (tenant_id, provider_id, employee),
  UNIQUE (tenant_id, provider_id, account_id),
  FOREIGN KEY (tenant_id, account_id) REFERENCES org.member(tenant_id,id) ON DELETE CASCADE
);
SELECT org.auriga_enable_tenant_rls('org.uac_employee_binding');

CREATE TABLE org.uac_invitation_employee (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  invitation_id uuid NOT NULL,
  provider_id uuid NOT NULL,
  employee text NOT NULL CHECK (length(employee) BETWEEN 1 AND 128),
  purpose text NOT NULL CHECK (purpose = 'uac_login'),
  verified_by uuid NOT NULL,
  verified_at timestamptz NOT NULL,
  UNIQUE (tenant_id, invitation_id),
  UNIQUE (tenant_id, provider_id, employee),
  FOREIGN KEY (tenant_id, invitation_id) REFERENCES org.invitation(tenant_id,id) ON DELETE CASCADE
);
SELECT org.auriga_enable_tenant_rls('org.uac_invitation_employee');
