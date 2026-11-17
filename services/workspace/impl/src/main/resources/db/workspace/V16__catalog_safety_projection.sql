ALTER TABLE instance ADD COLUMN catalog_admitted_spec bytea;
ALTER TABLE instance ADD COLUMN catalog_safety_spec bytea;
UPDATE instance SET catalog_admitted_spec=spec
WHERE catalog_policy_checked OR catalog_safety_generation=generation;
