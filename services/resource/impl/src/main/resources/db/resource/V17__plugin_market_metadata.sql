ALTER TABLE catalog_version ADD COLUMN market_author_name text;
ALTER TABLE catalog_version ADD COLUMN market_author_department text;
ALTER TABLE catalog_version ADD COLUMN market_published_at timestamptz;

CREATE TABLE catalog_plugin_star (
 tenant_id uuid NOT NULL,
 package_id uuid NOT NULL,
 account_id uuid NOT NULL,
 PRIMARY KEY (tenant_id, package_id, account_id),
 FOREIGN KEY (tenant_id, package_id) REFERENCES catalog_package(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_plugin_star');

-- Keep the latest positive/negative observation across changes of the desired version.
ALTER TABLE catalog_installation ADD COLUMN market_installed boolean;
ALTER TABLE catalog_installation DISABLE ROW LEVEL SECURITY;
UPDATE catalog_installation
 SET market_installed = (document->'report'->>'state' IN ('applied','disabled'))
 WHERE document->'report'->>'state' IN ('applied','disabled','removed');
ALTER TABLE catalog_installation ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_installation FORCE ROW LEVEL SECURITY;
CREATE INDEX catalog_market_installed_by_package
 ON catalog_installation(tenant_id, package_id, client_id) WHERE market_installed;

ALTER TABLE catalog_instance_installation ADD COLUMN market_installed boolean;
CREATE INDEX catalog_market_instance_installed_by_package
 ON catalog_instance_installation(tenant_id, package_id) WHERE market_installed;
