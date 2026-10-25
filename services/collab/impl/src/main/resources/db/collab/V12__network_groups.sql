-- Independent group authority. All mutations serialize through the tenant/realm fence;
-- immutable plan recipients are retained even when dispatch cannot create a mailbox item.
CREATE TABLE network_group_fence (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL,
  next_group numeric(20,0) NOT NULL DEFAULT 2 CHECK (next_group BETWEEN 2 AND 18446744073709551615),
  revision bigint NOT NULL DEFAULT 0, UNIQUE(tenant_id,realm)
);
CREATE TABLE network_group (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, local text NOT NULL,
  name text NOT NULL, owner_user text NOT NULL, revision bigint NOT NULL DEFAULT 1,
  admins_only boolean NOT NULL DEFAULT false, deleted boolean NOT NULL DEFAULT false,
  max_members integer NOT NULL DEFAULT 32 CHECK(max_members BETWEEN 1 AND 1024),
  max_nodes integer NOT NULL DEFAULT 8 CHECK(max_nodes BETWEEN 1 AND 256),
  UNIQUE(tenant_id,realm,local)
);
CREATE TABLE network_group_member (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, group_local text NOT NULL,
  node text NOT NULL, local text NOT NULL, address_type text NOT NULL,
  active boolean NOT NULL DEFAULT true, administrator boolean NOT NULL DEFAULT false,
  joined_at timestamptz NOT NULL, left_at timestamptz
);
CREATE UNIQUE INDEX network_group_member_active ON network_group_member(tenant_id,realm,group_local,node,local) WHERE active;
CREATE TABLE network_group_invitation (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, group_local text NOT NULL,
  group_revision bigint NOT NULL, membership_id uuid NOT NULL, contact_id uuid NOT NULL,
  node text NOT NULL, local text NOT NULL, address_type text NOT NULL,
  inviter_node text NOT NULL, inviter_local text NOT NULL,
  expires_at timestamptz NOT NULL, joined_revision bigint
);
CREATE TABLE network_group_human_intent (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, actor_json text NOT NULL,
  fingerprint text NOT NULL, expires_at timestamptz NOT NULL, consumed_message uuid
);
-- Human submission metadata does not duplicate the mailbox body.
CREATE TABLE network_human_submission (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, source_node text NOT NULL,
  idempotency_key text NOT NULL, fingerprint text NOT NULL, actor_json text NOT NULL,
  dispatch_status text NOT NULL, UNIQUE(tenant_id,realm,source_node,idempotency_key)
);
CREATE TABLE network_broadcast_grant (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, scope text NOT NULL,
  source_user text NOT NULL, node text, local text, binding text,
  expires_at timestamptz NOT NULL, max_nodes integer NOT NULL CHECK(max_nodes BETWEEN 1 AND 10000),
  max_bytes integer NOT NULL CHECK(max_bytes BETWEEN 1 AND 16384),
  revision bigint NOT NULL DEFAULT 1, revoked boolean NOT NULL DEFAULT false,
  CHECK(scope IN ('USER','NODE','AGENT')),
  CHECK(scope = 'USER' OR node IS NOT NULL),
  CHECK(scope <> 'AGENT' OR (local IS NOT NULL AND binding IS NOT NULL))
);
CREATE TABLE network_sender_freeze (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, scope text NOT NULL,
  subject text NOT NULL, revision bigint NOT NULL, frozen boolean NOT NULL,
  UNIQUE(tenant_id,realm,scope,subject)
);
CREATE TABLE network_group_plan (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, group_local text NOT NULL,
  position bigint GENERATED ALWAYS AS IDENTITY,
  source_node text NOT NULL, source_local text NOT NULL, source_user text NOT NULL,
  actor_json text NOT NULL, thread_id uuid NOT NULL, idempotency_key text NOT NULL, fingerprint text NOT NULL,
  body text NOT NULL, intent text NOT NULL, dispatch_mode text NOT NULL,
  total_members integer NOT NULL CHECK(total_members > 0), total_nodes integer NOT NULL CHECK(total_nodes > 0),
  grant_id uuid, grant_revision bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, response_deadline timestamptz NOT NULL,
  state_version bigint NOT NULL DEFAULT 1, recalled boolean NOT NULL DEFAULT false,
  UNIQUE(tenant_id,realm,source_node,source_local,idempotency_key)
);
CREATE TABLE network_group_recipient (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, message_id uuid NOT NULL,
  membership_id uuid NOT NULL, realm text NOT NULL, node text NOT NULL, local text NOT NULL, address_type text NOT NULL,
  dispatched boolean NOT NULL DEFAULT false, outcome text NOT NULL DEFAULT 'pending',
  terminal_at timestamptz,
  response text NOT NULL DEFAULT 'pending', response_revision integer NOT NULL DEFAULT 0,
  response_key text, response_kind text,
  UNIQUE(tenant_id,message_id,node,local),
  CHECK(outcome IN ('pending','accepted','rejected','expired','canceled','skipped','unresolved')),
  CHECK(response IN ('pending','responded','declined','timed_out','unavailable'))
);
CREATE INDEX network_group_recipient_page ON network_group_recipient(tenant_id,message_id,node) WHERE NOT dispatched AND outcome='pending';
CREATE INDEX network_group_recipient_member ON network_group_recipient(tenant_id,membership_id) WHERE outcome='pending';
CREATE TABLE network_group_response_operation (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, recipient_id uuid NOT NULL,
  idempotency_key text NOT NULL, revision integer NOT NULL, response text NOT NULL,
  UNIQUE(tenant_id,recipient_id,idempotency_key)
);
CREATE TABLE network_group_rate (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, scope text NOT NULL,
  subject text NOT NULL, tokens double precision NOT NULL, refilled_at timestamptz NOT NULL,
  UNIQUE(tenant_id,realm,scope,subject)
);
CREATE TABLE network_group_management (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, idempotency_key text NOT NULL,
  fingerprint text NOT NULL, result_revision bigint NOT NULL,
  UNIQUE(tenant_id,realm,idempotency_key)
);
CREATE TABLE network_group_state_update (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, message_id uuid NOT NULL, node text NOT NULL,
  revision bigint NOT NULL, kind text NOT NULL CHECK(kind IN ('recall','freeze')),
  scope text, subject text, frozen boolean,
  committed boolean NOT NULL DEFAULT false,
  CHECK(kind='recall' OR (scope IS NOT NULL AND subject IS NOT NULL AND frozen IS NOT NULL))
);
CREATE UNIQUE INDEX network_group_recall_once ON network_group_state_update(tenant_id,message_id,node,revision) WHERE kind='recall';
CREATE UNIQUE INDEX network_group_freeze_once ON network_group_state_update(tenant_id,message_id,node,revision,scope,subject) WHERE kind='freeze';
CREATE INDEX network_group_update_pending ON network_group_state_update(tenant_id,id) WHERE NOT committed;
CREATE TABLE network_group_freeze_dispatch (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, scope text NOT NULL, subject text NOT NULL,
  revision bigint NOT NULL, frozen boolean NOT NULL, boundary bigint NOT NULL,
  after_position bigint NOT NULL DEFAULT 0, after_node text NOT NULL DEFAULT '',
  complete boolean NOT NULL DEFAULT false, UNIQUE(tenant_id,realm,scope,subject)
);
CREATE TABLE network_group_audit (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, realm text NOT NULL, action text NOT NULL,
  actor_user text NOT NULL, subject text NOT NULL, reason text NOT NULL, created_at timestamptz NOT NULL
);
SELECT auriga_enable_tenant_rls('network_group_fence');
SELECT auriga_enable_tenant_rls('network_group');
SELECT auriga_enable_tenant_rls('network_group_member');
SELECT auriga_enable_tenant_rls('network_group_invitation');
SELECT auriga_enable_tenant_rls('network_group_human_intent');
SELECT auriga_enable_tenant_rls('network_human_submission');
SELECT auriga_enable_tenant_rls('network_broadcast_grant');
SELECT auriga_enable_tenant_rls('network_sender_freeze');
SELECT auriga_enable_tenant_rls('network_group_plan');
SELECT auriga_enable_tenant_rls('network_group_recipient');
SELECT auriga_enable_tenant_rls('network_group_response_operation');
SELECT auriga_enable_tenant_rls('network_group_rate');
SELECT auriga_enable_tenant_rls('network_group_management');
SELECT auriga_enable_tenant_rls('network_group_state_update');
SELECT auriga_enable_tenant_rls('network_group_freeze_dispatch');
SELECT auriga_enable_tenant_rls('network_group_audit');
