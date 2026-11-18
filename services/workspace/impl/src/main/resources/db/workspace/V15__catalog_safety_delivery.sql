ALTER TABLE instance ADD COLUMN catalog_safety_generation bigint;
ALTER TABLE instance ADD COLUMN catalog_assignment_retry boolean NOT NULL DEFAULT false;
