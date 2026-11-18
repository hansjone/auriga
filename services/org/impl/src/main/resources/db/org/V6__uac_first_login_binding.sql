-- Preserve the origin needed for IAM to resume MFA after an independently committed Org admission.
ALTER TABLE org.uac_employee_binding ADD COLUMN first_login boolean NOT NULL DEFAULT false;
