-- Generated relational keys keep the JSON contract payload and database integrity aligned.
ALTER TABLE iam.identity ADD COLUMN account_id uuid GENERATED ALWAYS AS ((data->>'accountId')::uuid) STORED NOT NULL,
 ADD COLUMN provider_id uuid GENERATED ALWAYS AS ((data->>'providerId')::uuid) STORED NOT NULL,
 ADD COLUMN subject text GENERATED ALWAYS AS (data->>'subject') STORED NOT NULL,
 ADD UNIQUE (tenant_id, provider_id, subject),
 ADD FOREIGN KEY (tenant_id, account_id) REFERENCES iam.account(tenant_id, id),
 ADD FOREIGN KEY (tenant_id, provider_id) REFERENCES iam.idp_provider(tenant_id, id);
ALTER TABLE iam.session ADD COLUMN account_id uuid GENERATED ALWAYS AS ((data->>'accountId')::uuid) STORED NOT NULL,
 ADD COLUMN provider_id uuid GENERATED ALWAYS AS ((data->>'providerId')::uuid) STORED NOT NULL,
 ADD FOREIGN KEY (tenant_id, account_id) REFERENCES iam.account(tenant_id, id),
 ADD FOREIGN KEY (tenant_id, provider_id) REFERENCES iam.idp_provider(tenant_id, id);
ALTER TABLE iam.emergency_account ADD COLUMN account_id uuid GENERATED ALWAYS AS ((data->>'accountId')::uuid) STORED NOT NULL,
 ADD FOREIGN KEY (tenant_id, account_id) REFERENCES iam.account(tenant_id, id);
ALTER TABLE iam.device ADD COLUMN account_id uuid GENERATED ALWAYS AS ((data->>'accountId')::uuid) STORED NOT NULL,
 ADD FOREIGN KEY (tenant_id, account_id) REFERENCES iam.account(tenant_id, id);
ALTER TABLE iam.login_tx ADD COLUMN provider_id uuid GENERATED ALWAYS AS ((data->>'providerId')::uuid) STORED NOT NULL,
 ADD FOREIGN KEY (tenant_id, provider_id) REFERENCES iam.idp_provider(tenant_id, id);