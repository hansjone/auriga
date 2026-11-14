-- The relay's existing budget ledger owns pricing and charging. This ledger
-- owns shared-session attribution, dispatch deduplication and uncertain usage.
CREATE TABLE ai.shared_model_call (
 tenant_id uuid NOT NULL,
 member_id uuid NOT NULL,
 binding_hash bytea NOT NULL CHECK (octet_length(binding_hash)=32),
 binding jsonb NOT NULL,
 call_id uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
 state text NOT NULL DEFAULT 'dispatching' CHECK (state IN ('dispatching','completed','failed','unknown')),
 input_tokens bigint CHECK (input_tokens BETWEEN 0 AND 9007199254740991),
 output_tokens bigint CHECK (output_tokens BETWEEN 0 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 PRIMARY KEY (tenant_id,binding_hash,call_id),
 CHECK ((state='dispatching' AND finished_at IS NULL AND input_tokens IS NULL AND output_tokens IS NULL)
     OR (state='unknown' AND finished_at IS NOT NULL AND input_tokens IS NULL AND output_tokens IS NULL)
     OR (state IN ('completed','failed') AND finished_at IS NOT NULL AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL))
);
CREATE INDEX shared_model_call_member ON ai.shared_model_call(tenant_id,member_id,created_at,call_id);
ALTER TABLE ai.shared_model_call ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai.shared_model_call FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai.shared_model_call
 USING (tenant_id=nullif(current_setting('auriga.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=nullif(current_setting('auriga.tenant_id',true),'')::uuid);
