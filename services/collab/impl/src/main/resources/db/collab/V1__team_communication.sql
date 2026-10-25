CREATE TABLE endpoint (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, account_id uuid NOT NULL,
  credential_kind text NOT NULL CHECK (credential_kind IN ('DEVICE','INSTANCE')),
  credential_subject uuid NOT NULL, label text NOT NULL CHECK (length(label) BETWEEN 1 AND 128),
  receive_enabled boolean NOT NULL, revoked boolean NOT NULL DEFAULT false,
  UNIQUE(tenant_id,id), UNIQUE(tenant_id,credential_kind,credential_subject)
);
CREATE TABLE session_binding (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, endpoint_id uuid NOT NULL,
  host_session_id text NOT NULL CHECK (length(host_session_id) BETWEEN 1 AND 512),
  UNIQUE(tenant_id,id,endpoint_id), UNIQUE(tenant_id,endpoint_id,host_session_id),
  FOREIGN KEY(tenant_id,endpoint_id) REFERENCES endpoint(tenant_id,id)
);
CREATE TABLE thread (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, next_sequence bigint NOT NULL DEFAULT 1,
  UNIQUE(tenant_id,id), CHECK(next_sequence > 0)
);
CREATE TABLE thread_participant (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, thread_id uuid NOT NULL,
  endpoint_id uuid NOT NULL, binding_id uuid,
  UNIQUE(tenant_id,thread_id,endpoint_id),
  FOREIGN KEY(tenant_id,thread_id) REFERENCES thread(tenant_id,id),
  FOREIGN KEY(tenant_id,endpoint_id) REFERENCES endpoint(tenant_id,id),
  FOREIGN KEY(tenant_id,binding_id,endpoint_id) REFERENCES session_binding(tenant_id,id,endpoint_id)
);
CREATE TABLE message (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, thread_id uuid NOT NULL,
  sequence bigint NOT NULL CHECK(sequence > 0), author_endpoint_id uuid NOT NULL,
  author_binding_id uuid NOT NULL, text text NOT NULL CHECK(octet_length(text) BETWEEN 1 AND 16384),
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,id), UNIQUE(tenant_id,id,thread_id),
  UNIQUE(tenant_id,thread_id,sequence),
  FOREIGN KEY(tenant_id,thread_id,author_endpoint_id) REFERENCES thread_participant(tenant_id,thread_id,endpoint_id),
  FOREIGN KEY(tenant_id,author_binding_id,author_endpoint_id) REFERENCES session_binding(tenant_id,id,endpoint_id)
);
CREATE TABLE delivery (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, message_id uuid NOT NULL,
  thread_id uuid NOT NULL, target_endpoint_id uuid NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','leased','host_accepted')),
  lease_token uuid, lease_expires_at timestamptz, host_request_id text,
  UNIQUE(tenant_id,message_id,target_endpoint_id),
  FOREIGN KEY(tenant_id,message_id,thread_id) REFERENCES message(tenant_id,id,thread_id),
  FOREIGN KEY(tenant_id,thread_id,target_endpoint_id) REFERENCES thread_participant(tenant_id,thread_id,endpoint_id)
);
CREATE TABLE request_dedup (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, endpoint_id uuid NOT NULL,
  binding_id uuid NOT NULL, operation text NOT NULL, key text NOT NULL,
  fingerprint text NOT NULL, message_id uuid NOT NULL,
  UNIQUE(tenant_id,endpoint_id,binding_id,operation,key),
  FOREIGN KEY(tenant_id,binding_id,endpoint_id) REFERENCES session_binding(tenant_id,id,endpoint_id),
  FOREIGN KEY(tenant_id,message_id) REFERENCES message(tenant_id,id)
);
CREATE INDEX delivery_pending ON delivery(tenant_id,target_endpoint_id,thread_id) WHERE status <> 'host_accepted';
SELECT auriga_enable_tenant_rls('endpoint');
SELECT auriga_enable_tenant_rls('session_binding');
SELECT auriga_enable_tenant_rls('thread');
SELECT auriga_enable_tenant_rls('thread_participant');
SELECT auriga_enable_tenant_rls('message');
SELECT auriga_enable_tenant_rls('delivery');
SELECT auriga_enable_tenant_rls('request_dedup');
