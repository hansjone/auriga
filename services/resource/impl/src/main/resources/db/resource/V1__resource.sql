CREATE TABLE resource_package (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  owner_id uuid NOT NULL,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 214),
  kind text NOT NULL DEFAULT 'plugin' CHECK (kind = 'plugin'),
  level text NOT NULL CHECK (level IN ('l0','l1','l2','l3','l4')),
  visibility text NOT NULL CHECK (visibility IN ('tenant','members','groups','private')),
  enabled boolean NOT NULL,
  default_version_id uuid,
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  created_at timestamptz NOT NULL,
  CHECK (level <> 'l4' OR visibility = 'private'),
  UNIQUE (tenant_id,id)
);
SELECT auriga_enable_tenant_rls('resource_package');
CREATE INDEX resource_package_page ON resource_package(tenant_id,id);

CREATE TABLE resource_package_group (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  package_id uuid NOT NULL,
  group_id uuid NOT NULL,
  UNIQUE (tenant_id,package_id,group_id),
  FOREIGN KEY (tenant_id,package_id) REFERENCES resource_package(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('resource_package_group');

CREATE TABLE resource_version (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  package_id uuid NOT NULL,
  author_id uuid NOT NULL,
  version text NOT NULL CHECK (char_length(version) BETWEEN 1 AND 128),
  digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
  size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 67108864),
  media_type text NOT NULL DEFAULT 'application/gzip' CHECK (media_type = 'application/gzip'),
  state text NOT NULL CHECK (state IN ('draft','pending_review','approved','rejected','published','deprecated','withdrawn')),
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  release_sequence bigint CHECK (release_sequence BETWEEN 1 AND 9007199254740991),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id,package_id,version),
  UNIQUE (tenant_id,package_id,id),
  FOREIGN KEY (tenant_id,package_id) REFERENCES resource_package(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('resource_version');
CREATE INDEX resource_version_page ON resource_version(tenant_id,package_id,id);
ALTER TABLE resource_package ADD CONSTRAINT resource_default_version
  FOREIGN KEY (tenant_id,id,default_version_id)
  REFERENCES resource_version(tenant_id,package_id,id) DEFERRABLE INITIALLY DEFERRED;
