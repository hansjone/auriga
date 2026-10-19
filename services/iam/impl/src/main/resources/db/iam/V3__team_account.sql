-- Team-account credentials, MFA, passkeys, and QR login challenges (B3).
CREATE TABLE iam."team_account" (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz, UNIQUE (tenant_id, lookup_key), UNIQUE (tenant_id, id));
SELECT iam.auriga_enable_tenant_rls('iam."team_account"');
CREATE INDEX team_account_expiry ON iam."team_account" (tenant_id, expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE iam."team_totp" (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz, UNIQUE (tenant_id, lookup_key), UNIQUE (tenant_id, id));
SELECT iam.auriga_enable_tenant_rls('iam."team_totp"');
CREATE INDEX team_totp_expiry ON iam."team_totp" (tenant_id, expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE iam."team_passkey" (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz, UNIQUE (tenant_id, lookup_key), UNIQUE (tenant_id, id));
SELECT iam.auriga_enable_tenant_rls('iam."team_passkey"');
CREATE INDEX team_passkey_expiry ON iam."team_passkey" (tenant_id, expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE iam."team_ceremony" (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz, UNIQUE (tenant_id, lookup_key), UNIQUE (tenant_id, id));
SELECT iam.auriga_enable_tenant_rls('iam."team_ceremony"');
CREATE INDEX team_ceremony_expiry ON iam."team_ceremony" (tenant_id, expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE iam."qr_challenge" (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz, UNIQUE (tenant_id, lookup_key), UNIQUE (tenant_id, id));
SELECT iam.auriga_enable_tenant_rls('iam."qr_challenge"');
CREATE INDEX qr_challenge_expiry ON iam."qr_challenge" (tenant_id, expires_at) WHERE expires_at IS NOT NULL;
