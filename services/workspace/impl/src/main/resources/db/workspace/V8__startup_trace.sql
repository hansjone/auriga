-- Current-generation startup cause, separate from the deterministic business spec.
-- Cleared on readiness, stop or terminal failure; existing rows have no historical cause.
ALTER TABLE instance ADD COLUMN startup_traceparent varchar(55);
