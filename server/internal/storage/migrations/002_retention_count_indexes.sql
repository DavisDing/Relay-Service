-- Fresh-install schema baseline. JSON remains the source of truth; an ordinary
-- timestamp index avoids the non-immutable text casts of an expression index.
ALTER TABLE relay_records ADD COLUMN retention_at TIMESTAMPTZ;

CREATE FUNCTION relay_set_retention_at() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog
AS $$
BEGIN
    IF NEW.bucket = 'monitor.history' THEN
        NEW.retention_at := NULLIF(NEW.value->>'completed_at', '')::timestamptz;
    ELSIF NEW.bucket = 'monitor.audit' THEN
        NEW.retention_at := NULLIF(NEW.value->>'at', '')::timestamptz;
    ELSE
        NEW.retention_at := NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER relay_records_retention_at
    BEFORE INSERT OR UPDATE ON relay_records
    FOR EACH ROW EXECUTE FUNCTION relay_set_retention_at();

CREATE INDEX relay_records_retention_at_idx
    ON relay_records (bucket, retention_at)
    WHERE retention_at IS NOT NULL;
