-- Keep historical employee verification for redemption retries, but reserve the
-- employee only while its invitation is usable. Provisioning expires reservations
-- with the application clock before creating a new one under the hierarchy lock.
LOCK TABLE org.invitation,org.uac_invitation_employee IN ACCESS EXCLUSIVE MODE;
ALTER TABLE org.uac_invitation_employee
 DROP CONSTRAINT uac_invitation_employee_tenant_id_provider_id_employee_key;
ALTER TABLE org.uac_invitation_employee ADD COLUMN active boolean NOT NULL DEFAULT true;
-- Transactional DDL holds exclusive locks until FORCE has been restored.
ALTER TABLE org.invitation NO FORCE ROW LEVEL SECURITY;
ALTER TABLE org.uac_invitation_employee NO FORCE ROW LEVEL SECURITY;
UPDATE org.uac_invitation_employee u SET active=false
 FROM org.invitation i WHERE i.tenant_id=u.tenant_id AND i.id=u.invitation_id
 AND (i.status<>'active' OR i.uses>=i.max_uses OR i.expires_at<=now());
ALTER TABLE org.uac_invitation_employee FORCE ROW LEVEL SECURITY;
ALTER TABLE org.invitation FORCE ROW LEVEL SECURITY;
CREATE UNIQUE INDEX uac_invitation_active_employee
 ON org.uac_invitation_employee(tenant_id,provider_id,employee) WHERE active;
CREATE FUNCTION org.uac_release_invitation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status<>'active' OR NEW.uses>=NEW.max_uses OR NEW.expires_at<=now() THEN
  UPDATE org.uac_invitation_employee SET active=false
   WHERE tenant_id=NEW.tenant_id AND invitation_id=NEW.id AND active;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER uac_invitation_release AFTER UPDATE OF status,uses,expires_at ON org.invitation
 FOR EACH ROW EXECUTE FUNCTION org.uac_release_invitation();
