CREATE TABLE org.egress_policy_delivery (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 digest text NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 published_at timestamptz NOT NULL,
 UNIQUE(tenant_id,version)
);
SELECT org.auriga_enable_tenant_rls('org.egress_policy_delivery');
CREATE TABLE org.egress_policy_report (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 version bigint NOT NULL,
 node_id text NOT NULL,
 digest text NOT NULL,
 result text NOT NULL CHECK(result IN ('applied','failed')),
 applied_version bigint NOT NULL CHECK(applied_version>=0 AND applied_version<=version),
 reported_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL,
 UNIQUE(tenant_id,version,node_id),
 FOREIGN KEY(tenant_id,version) REFERENCES org.egress_policy_delivery(tenant_id,version)
);
SELECT org.auriga_enable_tenant_rls('org.egress_policy_report');
