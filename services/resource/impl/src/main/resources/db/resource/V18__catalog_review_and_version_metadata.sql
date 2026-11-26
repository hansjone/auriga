ALTER TABLE catalog_version ADD COLUMN change_notes text;
ALTER TABLE catalog_review ADD COLUMN submit_reason text;
ALTER TABLE catalog_review ADD COLUMN created_at timestamptz;
ALTER TABLE catalog_review ADD COLUMN previous_published_version_id uuid;
ALTER TABLE catalog_decision ADD COLUMN reviewer_name text;
ALTER TABLE catalog_decision ADD COLUMN reviewer_department text;
