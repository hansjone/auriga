ALTER TABLE iam.session ADD COLUMN uac_credential_id uuid
 GENERATED ALWAYS AS ((data->>'uacCredentialId')::uuid) STORED;
ALTER TABLE iam.session ADD FOREIGN KEY(tenant_id,uac_credential_id)
 REFERENCES iam.uac_credential(tenant_id,id);
ALTER TABLE iam.session ADD CONSTRAINT uac_session_binding CHECK (
 (uac_credential_id IS NULL AND NOT data ? 'uacSessionBinding') OR
 (uac_credential_id IS NOT NULL AND COALESCE(
   (data->>'authGeneration')::bigint>0 AND
   (data->'uacSessionBinding'->>'authGeneration')::bigint=(data->>'authGeneration')::bigint AND
   data->'uacSessionBinding'->>'sourceSessionId'=data->>'sessionId' AND
   data->'uacSessionBinding'->>'providerId'=data->>'providerId',false))
);
CREATE INDEX session_uac_credential ON iam.session(tenant_id,uac_credential_id)
 WHERE uac_credential_id IS NOT NULL;
