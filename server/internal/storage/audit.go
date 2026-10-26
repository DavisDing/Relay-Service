package storage

import (
	"fmt"
	"time"
)

// AuditBucket is shared by business modules so authorization and monitoring
// changes retain a single ordered audit trail. It preserves the original bucket
// and sequence names for existing installations.
const AuditBucket = "monitor.audit"

// AuditRecord contains only stable identities and a fixed action name. Secret
// values, credential digests and untrusted display names must not be included.
type AuditRecord struct {
	ActorID  string    `json:"actor_id"`
	TargetID string    `json:"target_id"`
	Action   string    `json:"action"`
	At       time.Time `json:"at"`
}

// AppendAudit participates in the caller's transaction. A failure must abort
// that transaction, including the change the audit describes.
func AppendAudit(tx Tx, record AuditRecord) error {
	n, err := tx.Next(AuditBucket)
	if err != nil {
		return err
	}
	return tx.Put(AuditBucket, fmt.Sprintf("%020d", n), record)
}
