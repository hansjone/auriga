ALTER TABLE msgnet_realm ADD COLUMN transport_failure_code text NOT NULL DEFAULT 'none';
ALTER TABLE msgnet_projection_nonce ADD COLUMN superseded boolean NOT NULL DEFAULT false;
