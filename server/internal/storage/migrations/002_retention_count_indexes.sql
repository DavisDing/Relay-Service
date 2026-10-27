-- Retention previews count timestamped JSON records in the database instead
-- of paging and decoding every record in the application.
CREATE INDEX IF NOT EXISTS relay_history_completed_at_idx
    ON relay_records (bucket, (((NULLIF(value->>'completed_at', ''))::timestamptz)))
    WHERE bucket = 'monitor.history' AND value ? 'completed_at';

CREATE INDEX IF NOT EXISTS relay_audit_at_idx
    ON relay_records (bucket, (((NULLIF(value->>'at', ''))::timestamptz)))
    WHERE bucket = 'monitor.audit' AND value ? 'at';
