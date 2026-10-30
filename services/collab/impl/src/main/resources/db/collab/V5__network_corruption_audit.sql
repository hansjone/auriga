-- Immutable mailbox corruption is audited once per node mailbox item, not once per FETCH.
-- The marker and its outbox audit must commit or roll back in the same tenant transaction.
ALTER TABLE network_mail_item
  ADD COLUMN corruption_audited boolean NOT NULL DEFAULT false;
