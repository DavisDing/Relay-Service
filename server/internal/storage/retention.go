package storage

// retentionField is shared by SQLite writes and both indexed Count paths.
// PostgreSQL derives the column in its schema trigger to cover direct SQL and
// migration writes as well. TestRetentionMappingIntegration checks that durable
// SQL contract against this mapping; change it through a migration when needed.
func retentionField(bucket string) string {
	return retentionFields[bucket]
}

var retentionFields = map[string]string{
	"monitor.history": "completed_at",
	AuditBucket:       "at",
}
