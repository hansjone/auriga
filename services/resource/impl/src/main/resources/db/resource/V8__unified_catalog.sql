-- Additive migration. Operational rollback disables catalog writes; never drops evidence.
CREATE TABLE catalog_package (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, owner_id uuid NOT NULL,
 name text NOT NULL CHECK(name ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(name)<=64),
 kind text NOT NULL CHECK(kind IN ('skill','tool')),
 visibility text NOT NULL CHECK(visibility IN ('tenant','members','groups')),
 group_ids uuid[] NOT NULL DEFAULT '{}', enabled boolean NOT NULL DEFAULT false,
 default_version_id uuid, revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 release_high_water bigint NOT NULL DEFAULT 0 CHECK(release_high_water BETWEEN 0 AND 9007199254740991),
 created_at timestamptz NOT NULL,
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,kind,name)
);
SELECT auriga_enable_tenant_rls('catalog_package');
CREATE TABLE catalog_version (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, package_id uuid NOT NULL, author_id uuid NOT NULL,
 version text NOT NULL, digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 manifest_bytes bytea NOT NULL CHECK(octet_length(manifest_bytes) BETWEEN 1 AND 262144),
 manifest jsonb NOT NULL, state text NOT NULL CHECK(state IN ('draft','pending_review','approved','rejected','published','deprecated','withdrawn')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 minimum_risk text NOT NULL CHECK(minimum_risk IN ('r1','r2','r3')),
 risk text NOT NULL CHECK(risk IN ('r1','r2','r3') AND risk>=minimum_risk),
 release_sequence bigint CHECK(release_sequence BETWEEN 1 AND 9007199254740991), envelope jsonb,
 review_id uuid, created_at timestamptz NOT NULL,
 UNIQUE(tenant_id,package_id,id), UNIQUE(tenant_id,package_id,version),
 FOREIGN KEY(tenant_id,package_id) REFERENCES catalog_package(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_version');
ALTER TABLE catalog_package ADD CONSTRAINT catalog_default_version FOREIGN KEY(tenant_id,id,default_version_id)
 REFERENCES catalog_version(tenant_id,package_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE catalog_artifact (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, package_id uuid NOT NULL, version_id uuid NOT NULL,
 artifact_id text NOT NULL, digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 536870912), role text NOT NULL CHECK(role IN ('payload','source_snapshot','lockfile')),
 UNIQUE(tenant_id,version_id,artifact_id), FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES catalog_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_artifact');
CREATE INDEX catalog_artifact_digest ON catalog_artifact(tenant_id,digest);
CREATE TABLE catalog_review (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, package_id uuid NOT NULL, version_id uuid NOT NULL,
 version_revision bigint NOT NULL, digest text NOT NULL, status text NOT NULL CHECK(status IN ('pending','approved','rejected')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 UNIQUE(tenant_id,id), FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES catalog_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_review');
CREATE TABLE catalog_decision (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, review_id uuid NOT NULL, reviewer_id uuid NOT NULL,
 role text NOT NULL CHECK(role IN ('general','security')), decision text NOT NULL CHECK(decision IN ('approve','reject')),
 reason text NOT NULL, version_revision bigint NOT NULL, digest text NOT NULL, created_at timestamptz NOT NULL,
 FOREIGN KEY(tenant_id,review_id) REFERENCES catalog_review(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_decision');
CREATE TABLE catalog_migration_proof (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, package_id uuid NOT NULL, version_id uuid NOT NULL,
 release_sequence bigint NOT NULL CHECK(release_sequence BETWEEN 1 AND 9007199254740991),
 archive_digest text NOT NULL CHECK(archive_digest ~ '^sha256:[0-9a-f]{64}$'),
 manifest_digest text NOT NULL CHECK(manifest_digest ~ '^sha256:[0-9a-f]{64}$'), proof jsonb NOT NULL,
 activated_at timestamptz NOT NULL,
 UNIQUE(tenant_id,package_id,version_id,release_sequence),
 FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES resource_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_migration_proof');
-- No P1 inserts: plugin v2 remains disabled. security_invoker preserves caller RLS.
CREATE VIEW catalog_plugin_projection WITH (security_invoker=true) AS
 SELECT p.id AS package_id,p.tenant_id,p.owner_id,p.name,p.level,p.visibility,p.enabled,
 p.default_version_id,p.revision,p.release_high_water,v.id AS version_id,v.digest AS archive_digest,
 v.version,v.state,v.release_sequence,v.archive_verified
 FROM resource_package p LEFT JOIN resource_version v ON v.tenant_id=p.tenant_id AND v.package_id=p.id;

CREATE TABLE catalog_installation (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, owner_id uuid NOT NULL, client_id uuid NOT NULL, package_id uuid NOT NULL,
 version_id uuid NOT NULL, revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991), document jsonb NOT NULL,
 UNIQUE(tenant_id,owner_id,client_id,package_id),
 FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES catalog_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_installation');
CREATE TABLE catalog_ticket (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, owner_id uuid NOT NULL, client_id uuid NOT NULL, package_id uuid NOT NULL,
 version_id uuid NOT NULL, installation_revision bigint NOT NULL, ticket_hash text NOT NULL, expires_at timestamptz NOT NULL,
 UNIQUE(tenant_id,ticket_hash), FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES catalog_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_ticket');
CREATE TABLE catalog_distribution (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, actor_id uuid NOT NULL, instance_id uuid NOT NULL, owner_id uuid NOT NULL,
 node_id uuid NOT NULL, selection_revision bigint NOT NULL CHECK(selection_revision BETWEEN 1 AND 9007199254740991),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 fingerprint text NOT NULL, document jsonb NOT NULL, resolution jsonb NOT NULL,
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,instance_id,selection_revision)
);
SELECT auriga_enable_tenant_rls('catalog_distribution');

CREATE TABLE catalog_node_grant (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, distribution_id uuid NOT NULL, package_id uuid NOT NULL, version_id uuid NOT NULL,
 FOREIGN KEY(tenant_id,distribution_id) REFERENCES catalog_distribution(tenant_id,id),
 FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES catalog_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_node_grant');

CREATE TABLE catalog_revocation_delivery (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, instance_id uuid NOT NULL, revision bigint NOT NULL,
 completed_revision bigint NOT NULL DEFAULT 0, UNIQUE(tenant_id,instance_id)
);
SELECT auriga_enable_tenant_rls('catalog_revocation_delivery');
