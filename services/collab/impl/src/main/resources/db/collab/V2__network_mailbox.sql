-- Network mailbox / cursor / first-contact persistence (N1).
-- Legacy Team pull tables in V1 remain for the migration window.

CREATE TABLE network_node (
  tenant_id uuid NOT NULL,
  realm text NOT NULL CHECK (length(realm) BETWEEN 1 AND 32),
  node_id text NOT NULL CHECK (length(node_id) BETWEEN 1 AND 32),
  connection_epoch bigint NOT NULL DEFAULT 0 CHECK (connection_epoch >= 0),
  head_seq bigint NOT NULL DEFAULT 0 CHECK (head_seq >= 0),
  committed_cursor bigint NOT NULL DEFAULT 0 CHECK (committed_cursor >= 0),
  ingress_id text,
  down_subject text,
  route_expires_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, realm, node_id),
  CHECK (committed_cursor <= head_seq)
);

CREATE TABLE network_message (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL CHECK (length(realm) BETWEEN 1 AND 32),
  kind text NOT NULL CHECK (kind IN ('message', 'first_contact', 'reply')),
  body text NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 16384),
  source_node text NOT NULL CHECK (length(source_node) BETWEEN 1 AND 32),
  source_local text NOT NULL CHECK (length(source_local) BETWEEN 1 AND 32),
  thread_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id)
);

CREATE TABLE network_mail_item (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  message_id uuid NOT NULL,
  target_realm text NOT NULL CHECK (length(target_realm) BETWEEN 1 AND 32),
  target_node text NOT NULL CHECK (length(target_node) BETWEEN 1 AND 32),
  mail_seq bigint NOT NULL CHECK (mail_seq > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, message_id, target_realm, target_node),
  UNIQUE (tenant_id, target_realm, target_node, mail_seq),
  FOREIGN KEY (tenant_id, message_id) REFERENCES network_message (tenant_id, id)
);

CREATE TABLE network_recipient (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  target_local text NOT NULL CHECK (length(target_local) BETWEEN 1 AND 32),
  transport_status text NOT NULL DEFAULT 'pending'
    CHECK (transport_status IN (
      'pending', 'quarantined', 'host_accepted', 'expired',
      'canceled', 'revoked', 'skipped', 'unresolved'
    )),
  state_version bigint NOT NULL DEFAULT 1 CHECK (state_version > 0),
  attempt_count int NOT NULL DEFAULT 0 CHECK (attempt_count >= 0 AND attempt_count <= 6),
  admission_id uuid,
  admitted_epoch bigint,
  admitted_boot_id text,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, delivery_id, target_local),
  FOREIGN KEY (tenant_id, delivery_id) REFERENCES network_mail_item (tenant_id, id)
);

CREATE TABLE network_contact (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  message_id uuid NOT NULL,
  scope text NOT NULL DEFAULT 'conversation'
    CHECK (scope IN ('conversation', 'human_dialogue', 'group_join')),
  status text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'allowed', 'denied', 'expired')),
  source_realm text NOT NULL CHECK (length(source_realm) BETWEEN 1 AND 32),
  source_node text NOT NULL CHECK (length(source_node) BETWEEN 1 AND 32),
  source_local text NOT NULL CHECK (length(source_local) BETWEEN 1 AND 32),
  target_realm text NOT NULL CHECK (length(target_realm) BETWEEN 1 AND 32),
  target_node text NOT NULL CHECK (length(target_node) BETWEEN 1 AND 32),
  expires_at timestamptz NOT NULL,
  decision_version bigint NOT NULL DEFAULT 1 CHECK (decision_version > 0),
  status_viewer boolean NOT NULL DEFAULT false,
  decided_at timestamptz,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, message_id),
  FOREIGN KEY (tenant_id, message_id) REFERENCES network_message (tenant_id, id)
);

CREATE INDEX network_mail_item_node_seq
  ON network_mail_item (tenant_id, target_realm, target_node, mail_seq);
CREATE INDEX network_recipient_delivery
  ON network_recipient (tenant_id, delivery_id);
CREATE INDEX network_contact_target_pending
  ON network_contact (tenant_id, target_realm, target_node)
  WHERE status = 'pending';

SELECT auriga_enable_tenant_rls('network_node');
SELECT auriga_enable_tenant_rls('network_message');
SELECT auriga_enable_tenant_rls('network_mail_item');
SELECT auriga_enable_tenant_rls('network_recipient');
SELECT auriga_enable_tenant_rls('network_contact');
