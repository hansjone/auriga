-- Ordering belongs to the accepted observation generation, not to desired-spec updated_at.
-- Existing observations conservatively fall back to their server-side updated_at until refreshed.
ALTER TABLE instance ADD COLUMN status_watermark timestamptz;
