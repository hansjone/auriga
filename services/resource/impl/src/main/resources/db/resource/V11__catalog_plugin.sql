-- Unified plugins use independent catalog IDs and v2 manifests. Legacy tables remain intact.
ALTER TABLE catalog_package DROP CONSTRAINT catalog_package_kind_check;
ALTER TABLE catalog_package ADD CONSTRAINT catalog_package_kind_check CHECK(kind IN ('plugin','skill','tool'));
