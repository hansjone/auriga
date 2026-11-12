-- Flyway retains the DDL lock to transaction commit; restore forced RLS after
-- removing credentials from every historical tenant's completed ticket receipts.
ALTER TABLE resource_operation DISABLE ROW LEVEL SECURITY;
UPDATE resource_operation SET response='{"completed":true}'::jsonb
WHERE operation LIKE 'member-ticket/%';
ALTER TABLE resource_operation ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_operation FORCE ROW LEVEL SECURITY;

ALTER TABLE resource_distribution DROP CONSTRAINT resource_distribution_state_check;
ALTER TABLE resource_distribution ADD CONSTRAINT resource_distribution_state_check
CHECK (state IN ('requested','resolved','applying','applied','failed','superseded'));
