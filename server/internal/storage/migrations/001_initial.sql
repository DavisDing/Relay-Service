CREATE TABLE IF NOT EXISTS relay_records (
    bucket TEXT NOT NULL CHECK (bucket <> ''),
    record_key TEXT NOT NULL CHECK (record_key <> ''),
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket, record_key)
);

CREATE INDEX IF NOT EXISTS relay_records_bucket_key_idx
    ON relay_records (bucket, record_key COLLATE "C");

CREATE TABLE IF NOT EXISTS relay_sequences (
    sequence_name TEXT PRIMARY KEY,
    next_value BIGINT NOT NULL CHECK (next_value > 0)
);

-- Deployed application identity may mutate only business records/sequences.
-- The migration ledger remains read-only for the regular process.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_app') THEN
        GRANT SELECT ON relay_schema_migrations TO relay_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON relay_records, relay_sequences TO relay_app;
    END IF;
END $$;
