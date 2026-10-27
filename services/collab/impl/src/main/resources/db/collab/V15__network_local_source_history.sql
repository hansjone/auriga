-- Old operations have no authoritative redeem provenance. Never backfill it from current state.
ALTER TABLE network_source_operation ADD COLUMN local_history_version integer;
ALTER TABLE network_source_operation ADD COLUMN original_connection_anchor bytea;
ALTER TABLE network_source_operation ADD COLUMN local_history_digest text;

CREATE FUNCTION network_source_local_history_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER
SET search_path=collab,pg_temp
AS $$
BEGIN
  IF TG_OP <> 'INSERT' AND OLD.status='not_accepted' AND OLD.iam_request IS NULL THEN
    RAISE EXCEPTION 'local source history is immutable' USING ERRCODE='23514';
  END IF;
  IF TG_OP <> 'DELETE' AND NEW.status='not_accepted' AND NEW.iam_request IS NULL THEN
    IF NEW.method <> 'Register' OR NEW.local_history_version IS DISTINCT FROM 1
       OR NEW.original_connection_anchor IS NULL OR octet_length(NEW.original_connection_anchor)=0
       OR NEW.local_history_digest IS NULL
       OR NEW.local_history_digest !~ '^sha256:[0-9a-f]{64}$' THEN
      RAISE EXCEPTION 'new local source history requires retained provenance' USING ERRCODE='23514';
    END IF;
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION network_source_local_history_guard() FROM PUBLIC;
CREATE TRIGGER network_source_local_history_guard BEFORE INSERT OR UPDATE OR DELETE
ON network_source_operation FOR EACH ROW EXECUTE FUNCTION network_source_local_history_guard();
