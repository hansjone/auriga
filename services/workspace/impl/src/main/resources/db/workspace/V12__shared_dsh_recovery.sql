-- Add recovery to the authority without rewriting prior migrations.
ALTER TABLE shared_dsh_pool DROP CONSTRAINT shared_dsh_pool_state_check;
ALTER TABLE shared_dsh_pool ADD CONSTRAINT shared_dsh_pool_state_check CHECK(state IN ('pending','ready','blocked','retired'));
ALTER TABLE shared_dsh_operation ADD CONSTRAINT shared_dsh_operation_state_check
 CHECK(state IN ('accepted','fencing','draining','preparing','committing','succeeded','failed','blocked'));
ALTER TABLE shared_dsh_operation ADD CONSTRAINT shared_dsh_operation_action_check
 CHECK(action IN ('cooperative-cancel','stop-pool','discard-sessions','archive-reset','prepare-empty'));
ALTER TABLE shared_dsh_operation ADD COLUMN source_stopped boolean NOT NULL DEFAULT false;
ALTER TABLE shared_dsh_operation ADD COLUMN reason text NOT NULL DEFAULT 'legacy operation';
ALTER TABLE shared_dsh_operation ADD COLUMN step_up_ref text NOT NULL DEFAULT 'legacy';
ALTER TABLE shared_dsh_operation ADD COLUMN recovery_id uuid UNIQUE;
ALTER TABLE shared_dsh_operation ADD COLUMN recovery_pending boolean NOT NULL DEFAULT false;
ALTER TABLE shared_dsh_operation ADD COLUMN recovery_stage text;
ALTER TABLE shared_dsh_operation ADD COLUMN recovery_intent bytea;
ALTER TABLE shared_dsh_operation ADD COLUMN rollback_pool uuid;
ALTER TABLE shared_dsh_operation ADD COLUMN recovery_started_at bigint;
ALTER TABLE shared_dsh_operation ADD COLUMN recovery_epoch bigint;
ALTER TABLE shared_dsh_maintenance ADD COLUMN failure_code text;
CREATE TABLE shared_dsh_pool_stop (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, pool_id uuid NOT NULL, intent bytea NOT NULL,
 reason text NOT NULL, receipt bytea, state text NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','stopped','retired')),
 created_at bigint NOT NULL, FOREIGN KEY(tenant_id,pool_id) REFERENCES shared_dsh_pool(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('shared_dsh_pool_stop');
CREATE TABLE shared_dsh_audit (
 id uuid PRIMARY KEY,tenant_id uuid NOT NULL,operation_id uuid NOT NULL,result text NOT NULL,
 payload jsonb NOT NULL,created_at bigint NOT NULL
);
SELECT auriga_enable_tenant_rls('shared_dsh_audit');
