ALTER TABLE resource_version ADD COLUMN manifest jsonb;
ALTER TABLE resource_version ADD COLUMN files jsonb;
ALTER TABLE resource_version ADD COLUMN archive_verified boolean NOT NULL DEFAULT false;
ALTER TABLE resource_version ADD COLUMN envelope jsonb;
ALTER TABLE resource_package ADD COLUMN release_high_water bigint NOT NULL DEFAULT 0 CHECK (release_high_water BETWEEN 0 AND 9007199254740991);
UPDATE resource_package p SET release_high_water=COALESCE((SELECT max(v.release_sequence) FROM resource_version v WHERE v.tenant_id=p.tenant_id AND v.package_id=p.id),0);

CREATE TABLE resource_review (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, package_id uuid NOT NULL, version_id uuid NOT NULL,
  digest text NOT NULL, version_revision bigint NOT NULL, state text NOT NULL CHECK (state IN ('pending','approved','rejected')),
  revision bigint NOT NULL DEFAULT 1, submitter_id uuid NOT NULL, reviewer_id uuid, reason text NOT NULL,
  created_at timestamptz NOT NULL, decided_at timestamptz,
  FOREIGN KEY (tenant_id,package_id,version_id) REFERENCES resource_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('resource_review');
CREATE INDEX resource_review_queue ON resource_review(tenant_id,id);

CREATE TABLE resource_operation (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, actor_id uuid NOT NULL, operation text NOT NULL,
  request_key text NOT NULL, fingerprint text NOT NULL, response jsonb NOT NULL, expires_at timestamptz NOT NULL,
  UNIQUE(tenant_id,actor_id,operation,request_key)
);
SELECT auriga_enable_tenant_rls('resource_operation');

CREATE TABLE resource_revocations (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL UNIQUE, revision bigint NOT NULL, document jsonb NOT NULL
);
SELECT auriga_enable_tenant_rls('resource_revocations');

CREATE TABLE resource_distribution (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, actor_id uuid NOT NULL, instance_id uuid NOT NULL,
  owner_id uuid NOT NULL, administrator boolean NOT NULL DEFAULT false, selection_revision bigint NOT NULL, selections jsonb NOT NULL,
  state text NOT NULL CHECK (state IN ('requested','resolved','applying','applied','failed')),
  generation bigint, observed_generation bigint, ready boolean, error_code text, revision bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL, reconciled_at timestamptz NOT NULL DEFAULT 'epoch',
  UNIQUE(tenant_id,instance_id,selection_revision)
);
SELECT auriga_enable_tenant_rls('resource_distribution');

CREATE TABLE resource_download (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, node_id uuid, instance_id uuid NOT NULL,
  owner_id uuid NOT NULL, package_id uuid NOT NULL, version_id uuid NOT NULL, digest text NOT NULL,
  selection_revision bigint NOT NULL, expires_at timestamptz NOT NULL
);
SELECT auriga_enable_tenant_rls('resource_download');
