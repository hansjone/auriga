-- Portal CSRF proof lookup never scans unrelated sessions.
CREATE INDEX session_portal_csrf ON iam.session(tenant_id,(data->>'csrfHash'))
 WHERE data->>'audience'='portal' AND data->>'revoked'='false';
CREATE INDEX uac_navigation_binding ON iam.uac_navigation(tenant_id,(data->>'bindingHash'));
CREATE INDEX uac_challenge_binding ON iam.uac_challenge(tenant_id,(data->>'bindingHash'));
CREATE INDEX uac_challenge_provider ON iam.uac_challenge(tenant_id,provider_id);
CREATE INDEX login_tx_provider ON iam.login_tx(tenant_id,provider_id);
CREATE INDEX login_tx_binding ON iam.login_tx(tenant_id,(data->>'bindingHash'));
CREATE INDEX login_tx_uac_expiry ON iam.login_tx(tenant_id,expires_at,id)
 WHERE jsonb_exists(data,'uacPendingAccountId');
CREATE INDEX session_provider ON iam.session(tenant_id,provider_id);
CREATE INDEX identity_account_lookup ON iam.identity(tenant_id,(data->>'accountId'));
CREATE INDEX device_source_session ON iam.device(tenant_id,(data->>'sessionId'));
CREATE INDEX oauth_grant_source_session ON iam.oauth_grant(tenant_id,(data->>'sessionId'));
CREATE INDEX oauth_grant_pending_user_code ON iam.oauth_grant
 (tenant_id,(replace(data->>'userCode','-','')),expires_at)
 WHERE data->>'kind'='device' AND data->>'status'='pending'
 AND data->>'used' IS DISTINCT FROM 'true' AND data->>'revoked' IS DISTINCT FROM 'true'
 AND data->>'authorizationMode' IS DISTINCT FROM 'native_tx';
CREATE INDEX uac_credential_token_lookup ON iam.uac_credential
 (tenant_id,provider_id,(data->>'accountId'),(data->>'tokenHash'))
 WHERE data->>'kind'='credential' AND data->>'invalid'='false';
CREATE INDEX uac_job_pending_due ON iam.uac_job(tenant_id,next_at,id)
 WHERE data->>'done' IS DISTINCT FROM 'true';

-- Timestamp casts are not immutable expressions; materialize the scheduler column.
ALTER TABLE iam.uac_credential ADD COLUMN retry_deadline timestamptz;
ALTER TABLE iam.uac_credential ADD COLUMN cleanup_at timestamptz;
CREATE FUNCTION iam.uac_retention_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.data ? 'expiresAt' THEN
  NEW.expires_at := (NEW.data->>'expiresAt')::timestamptz;
 END IF;
 IF TG_TABLE_NAME='uac_credential' THEN
  NEW.retry_deadline := (NEW.data->>'retryDeadline')::timestamptz;
  IF NEW.cleanup_at IS NULL AND NEW.data->>'kind'='credential' THEN
   NEW.cleanup_at := COALESCE((NEW.data->>'updatedAt')::timestamptz,now())+interval '1 day';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER uac_credential_retention BEFORE INSERT OR UPDATE ON iam.uac_credential
 FOR EACH ROW EXECUTE FUNCTION iam.uac_retention_schedule();
CREATE TRIGGER uac_job_retention BEFORE INSERT OR UPDATE ON iam.uac_job
 FOR EACH ROW EXECUTE FUNCTION iam.uac_retention_schedule();
-- Device-code lookup must use the same expiry after any authority update.
CREATE TRIGGER oauth_grant_expiry BEFORE INSERT OR UPDATE ON iam.oauth_grant
 FOR EACH ROW EXECUTE FUNCTION iam.uac_retention_schedule();
-- Like V7: the migration owner backfills under a transaction-held exclusive
-- table lock; runtime clients never observe relaxed FORCE RLS.
ALTER TABLE iam.oauth_grant NO FORCE ROW LEVEL SECURITY;
UPDATE iam.oauth_grant SET expires_at=(data->>'expiresAt')::timestamptz
 WHERE jsonb_exists(data,'expiresAt');
ALTER TABLE iam.oauth_grant FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.uac_credential NO FORCE ROW LEVEL SECURITY;
UPDATE iam.uac_credential SET retry_deadline=(data->>'retryDeadline')::timestamptz;
UPDATE iam.uac_credential SET data=jsonb_set(data,'{expiresAt}',to_jsonb((now()+interval '1 day')::text))
 WHERE data->>'invalid'='true' AND expires_at IS NULL;
ALTER TABLE iam.uac_credential FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.uac_job NO FORCE ROW LEVEL SECURITY;
UPDATE iam.uac_job SET data=jsonb_set(data,'{expiresAt}',to_jsonb((now()+interval '5 minutes')::text))
 WHERE data->>'done'='true' AND expires_at IS NULL;
ALTER TABLE iam.uac_job FORCE ROW LEVEL SECURITY;
CREATE INDEX uac_credential_retry_due ON iam.uac_credential(tenant_id,retry_deadline,id)
 WHERE data->>'invalid'='false';
CREATE INDEX uac_credential_cleanup_due ON iam.uac_credential(tenant_id,cleanup_at,id)
 WHERE data->>'kind'='credential' AND data->>'invalid'='false';
CREATE INDEX uac_credential_retired ON iam.uac_credential(tenant_id,expires_at,id)
 WHERE data->>'invalid'='true';
CREATE INDEX uac_challenge_expiry ON iam.uac_challenge(tenant_id,expires_at,id);
CREATE INDEX uac_navigation_expiry ON iam.uac_navigation(tenant_id,expires_at,id);
CREATE INDEX uac_suppression_expiry ON iam.uac_suppression(tenant_id,expires_at,id);
CREATE INDEX uac_job_expiry ON iam.uac_job(tenant_id,expires_at,id);
CREATE INDEX uac_credential_encryption_key ON iam.uac_credential
 (tenant_id,(data->'envelope'->>'keyId'),id);
CREATE INDEX uac_challenge_credential_key ON iam.uac_challenge
 (tenant_id,(data->'credential'->>'keyId'),id) WHERE jsonb_exists(data,'credential');
CREATE INDEX uac_challenge_recovery_key ON iam.uac_challenge
 (tenant_id,(data->'issued'->>'keyId'),id) WHERE jsonb_exists(data,'issued');
CREATE INDEX login_tx_uac_key ON iam.login_tx
 (tenant_id,(data->'uacCredential'->>'keyId'),id) WHERE jsonb_exists(data,'uacCredential');
CREATE INDEX uac_navigation_response_key ON iam.uac_navigation
 (tenant_id,(data->'response'->>'keyId'),id) WHERE jsonb_exists(data,'response');
