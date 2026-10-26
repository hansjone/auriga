-- IAM's committed observation of tenant policy, not an org-wide revision or connection identity.
CREATE TABLE iam.network_policy_revision (
  tenant_id uuid PRIMARY KEY,
  revision numeric(20,0) NOT NULL CHECK (revision BETWEEN 1 AND 18446744073709551615)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_policy_revision');
