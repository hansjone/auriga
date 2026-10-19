-- org schema: tenants, members, groups, roles, policies, quotas, invitations.
-- Common migrations install auriga_enable_tenant_rls first.

CREATE TABLE org.tenant (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  slug text NOT NULL,
  display_name text NOT NULL,
  locale text NOT NULL DEFAULT 'en',
  hosts text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, slug)
);
SELECT org.auriga_enable_tenant_rls('org.tenant');

CREATE TABLE org.member (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  display_name text NOT NULL,
  email text,
  member_type text NOT NULL CHECK (member_type IN ('member', 'external')),
  status text NOT NULL CHECK (status IN ('active', 'disabled', 'offboarded')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  disabled_at timestamptz,
  offboarded_at timestamptz,
  UNIQUE (tenant_id, id)
);
SELECT org.auriga_enable_tenant_rls('org.member');
CREATE INDEX member_status_idx ON org.member (tenant_id, status);
CREATE INDEX member_email_idx ON org.member (tenant_id, lower(email)) WHERE email IS NOT NULL;

CREATE TABLE org."group" (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  name text NOT NULL,
  description text,
  parent_id uuid,
  source text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'mapped')),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, name)
);
SELECT org.auriga_enable_tenant_rls('org."group"');

CREATE TABLE org.group_member (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  group_id uuid NOT NULL,
  account_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, group_id, account_id)
);
SELECT org.auriga_enable_tenant_rls('org.group_member');
CREATE INDEX group_member_account_idx ON org.group_member (tenant_id, account_id);

CREATE TABLE org.permission (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  key text NOT NULL,
  service text NOT NULL,
  description_en text NOT NULL,
  description_zh text NOT NULL,
  sensitive boolean NOT NULL DEFAULT false,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, key)
);
SELECT org.auriga_enable_tenant_rls('org.permission');

CREATE TABLE org.role (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  key text NOT NULL,
  name_en text NOT NULL,
  name_zh text NOT NULL,
  description_en text,
  description_zh text,
  builtin boolean NOT NULL DEFAULT false,
  permissions text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, key)
);
SELECT org.auriga_enable_tenant_rls('org.role');

CREATE TABLE org.role_binding (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  role_id uuid NOT NULL,
  subject_type text NOT NULL CHECK (subject_type IN ('member', 'group', 'service_account')),
  subject_id uuid NOT NULL,
  granted_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, role_id, subject_type, subject_id)
);
SELECT org.auriga_enable_tenant_rls('org.role_binding');
CREATE INDEX role_binding_subject_idx ON org.role_binding (tenant_id, subject_type, subject_id);

CREATE TABLE org.policy (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  key text NOT NULL CHECK (key IN ('workspace', 'egress', 'login')),
  version bigint NOT NULL DEFAULT 1,
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, key)
);
SELECT org.auriga_enable_tenant_rls('org.policy');

CREATE TABLE org.quota (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  scope text NOT NULL CHECK (scope IN ('default', 'group', 'member')),
  scope_id uuid,
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id)
);
SELECT org.auriga_enable_tenant_rls('org.quota');
CREATE UNIQUE INDEX quota_default_uniq ON org.quota (tenant_id) WHERE scope = 'default';
CREATE UNIQUE INDEX quota_scoped_uniq ON org.quota (tenant_id, scope, scope_id) WHERE scope_id IS NOT NULL;

CREATE TABLE org.invitation (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  code_hash text NOT NULL,
  status text NOT NULL CHECK (status IN ('active', 'used', 'expired', 'revoked')),
  email text,
  display_name text,
  member_type text NOT NULL CHECK (member_type IN ('member', 'external')),
  role_ids uuid[] NOT NULL DEFAULT '{}',
  group_ids uuid[] NOT NULL DEFAULT '{}',
  provider_ids uuid[] NOT NULL DEFAULT '{}',
  max_uses int NOT NULL DEFAULT 1 CHECK (max_uses >= 1),
  uses int NOT NULL DEFAULT 0 CHECK (uses >= 0),
  expires_at timestamptz NOT NULL,
  created_by uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, code_hash)
);
SELECT org.auriga_enable_tenant_rls('org.invitation');

CREATE TABLE org.invitation_redemption (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  invitation_id uuid NOT NULL,
  account_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, invitation_id, account_id)
);
SELECT org.auriga_enable_tenant_rls('org.invitation_redemption');

-- Seed under the default tenant so FORCE RLS WITH CHECK accepts the rows.
SELECT set_config('auriga.tenant_id', '00000000-0000-0000-0000-000000000001', true);

-- Default private-deployment tenant (matches iam host map examples).
INSERT INTO org.tenant (id, tenant_id, slug, display_name, locale, hosts, created_at)
VALUES (
  '00000000-0000-0000-0000-000000000001',
  '00000000-0000-0000-0000-000000000001',
  'default',
  'Default',
  'zh-CN',
  ARRAY[]::text[],
  now()
);

-- Permission catalog (org domain only; other services register later).
INSERT INTO org.permission (id, tenant_id, key, service, description_en, description_zh, sensitive)
SELECT id, '00000000-0000-0000-0000-000000000001', key, service, description_en, description_zh, sensitive
FROM (VALUES
  ('00000000-0000-7000-9000-000000000001'::uuid, 'org.tenant.write', 'org', 'Update tenant settings', '更新租户设置', true),
  ('00000000-0000-7000-9000-000000000002'::uuid, 'org.members.read', 'org', 'Read members', '查看成员', false),
  ('00000000-0000-7000-9000-000000000003'::uuid, 'org.members.write', 'org', 'Manage members', '管理成员', true),
  ('00000000-0000-7000-9000-000000000004'::uuid, 'org.members.offboard', 'org', 'Offboard members', '办理离职', true),
  ('00000000-0000-7000-9000-000000000005'::uuid, 'org.groups.read', 'org', 'Read groups', '查看组', false),
  ('00000000-0000-7000-9000-000000000006'::uuid, 'org.groups.write', 'org', 'Manage groups', '管理组', true),
  ('00000000-0000-7000-9000-000000000007'::uuid, 'org.roles.read', 'org', 'Read roles', '查看角色', false),
  ('00000000-0000-7000-9000-000000000008'::uuid, 'org.roles.write', 'org', 'Manage roles', '管理角色', true),
  ('00000000-0000-7000-9000-000000000009'::uuid, 'org.roles.grant', 'org', 'Grant and revoke roles', '授予与撤销角色', true),
  ('00000000-0000-7000-9000-00000000000a'::uuid, 'org.policies.read', 'org', 'Read policies', '查看策略', false),
  ('00000000-0000-7000-9000-00000000000b'::uuid, 'org.policies.write', 'org', 'Manage policies', '管理策略', true),
  ('00000000-0000-7000-9000-00000000000c'::uuid, 'org.quotas.read', 'org', 'Read quotas', '查看配额', false),
  ('00000000-0000-7000-9000-00000000000d'::uuid, 'org.quotas.write', 'org', 'Manage quotas', '管理配额', true),
  ('00000000-0000-7000-9000-00000000000e'::uuid, 'org.invitations.read', 'org', 'Read invitations', '查看邀请', false),
  ('00000000-0000-7000-9000-00000000000f'::uuid, 'org.invitations.write', 'org', 'Manage invitations', '管理邀请', true)
) AS p(id, key, service, description_en, description_zh, sensitive);

-- Builtin roles.
INSERT INTO org.role (id, tenant_id, key, name_en, name_zh, description_en, description_zh, builtin, permissions)
VALUES
(
  '00000000-0000-7000-8000-000000000001',
  '00000000-0000-0000-0000-000000000001',
  'owner', 'Owner', '超管', 'Full organization control', '组织全部权限', true,
  ARRAY[
    'org.tenant.write','org.members.read','org.members.write','org.members.offboard',
    'org.groups.read','org.groups.write','org.roles.read','org.roles.write','org.roles.grant',
    'org.policies.read','org.policies.write','org.quotas.read','org.quotas.write',
    'org.invitations.read','org.invitations.write'
  ]::text[]
),
(
  '00000000-0000-7000-8000-000000000002',
  '00000000-0000-0000-0000-000000000001',
  'admin', 'Admin', '管理员', 'Organization administration', '组织管理', true,
  ARRAY[
    'org.members.read','org.members.write','org.members.offboard',
    'org.groups.read','org.groups.write','org.roles.read','org.roles.write','org.roles.grant',
    'org.policies.read','org.policies.write','org.quotas.read','org.quotas.write',
    'org.invitations.read','org.invitations.write'
  ]::text[]
),
(
  '00000000-0000-7000-8000-000000000003',
  '00000000-0000-0000-0000-000000000001',
  'member', 'Member', '成员', 'Standard member', '普通成员', true,
  ARRAY[]::text[]
),
(
  '00000000-0000-7000-8000-000000000004',
  '00000000-0000-0000-0000-000000000001',
  'external', 'External', '外部协作者', 'External collaborator', '外部协作者', true,
  ARRAY[]::text[]
);

INSERT INTO org.policy (id, tenant_id, key, version, value, updated_at)
VALUES
(
  '00000000-0000-7000-a000-000000000001',
  '00000000-0000-0000-0000-000000000001',
  'workspace', 1,
  '{"idleMinutes":30,"wakeOnSchedule":true,"alwaysOnRequiresApproval":true,"maxConcurrentStarts":4,"allowPersonalPlugins":true}'::jsonb,
  now()
),
(
  '00000000-0000-7000-a000-000000000002',
  '00000000-0000-0000-0000-000000000001',
  'egress', 1,
  '{"categories":[],"allowDomains":[],"denyDomains":[]}'::jsonb,
  now()
),
(
  '00000000-0000-7000-a000-000000000003',
  '00000000-0000-0000-0000-000000000001',
  'login', 1,
  '{"requireMfa":false,"adminRequireMfa":true,"maxSessionsPerAccount":20}'::jsonb,
  now()
);

INSERT INTO org.quota (id, tenant_id, scope, scope_id, value, updated_at)
VALUES (
  '00000000-0000-7000-b000-000000000001',
  '00000000-0000-0000-0000-000000000001',
  'default',
  NULL,
  '{}'::jsonb,
  now()
);

-- Pre-login host lookup. FORCE RLS means even the table owner cannot SET row_security=off;
-- bind the well-known default tenant (private deploy) so the policy passes. Multi-tenant SaaS
-- host indexing is out of scope for B6.
CREATE OR REPLACE FUNCTION org.resolve_tenant_by_host(p_host text)
RETURNS TABLE (
  id uuid,
  slug text,
  display_name text,
  locale text,
  hosts text[],
  created_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = org
AS $$
DECLARE
  default_id uuid := '00000000-0000-0000-0000-000000000001';
BEGIN
  PERFORM set_config('auriga.tenant_id', default_id::text, true);
  RETURN QUERY
    SELECT t.id, t.slug, t.display_name, t.locale, t.hosts, t.created_at
    FROM org.tenant t
    WHERE p_host = ANY (t.hosts)
    LIMIT 1;
END;
$$;

CREATE OR REPLACE FUNCTION org.default_tenant()
RETURNS TABLE (
  id uuid,
  slug text,
  display_name text,
  locale text,
  hosts text[],
  created_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = org
AS $$
DECLARE
  default_id uuid := '00000000-0000-0000-0000-000000000001';
BEGIN
  PERFORM set_config('auriga.tenant_id', default_id::text, true);
  RETURN QUERY
    SELECT t.id, t.slug, t.display_name, t.locale, t.hosts, t.created_at
    FROM org.tenant t
    WHERE t.id = default_id
    LIMIT 1;
END;
$$;

GRANT EXECUTE ON FUNCTION org.resolve_tenant_by_host(text) TO PUBLIC;
GRANT EXECUTE ON FUNCTION org.default_tenant() TO PUBLIC;
