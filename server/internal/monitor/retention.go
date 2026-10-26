package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"relayservice/internal/storage"
)

const retentionBucket = "monitor.retention"
const retentionBatchSize = 200

// RetentionPolicy is opt-in. Zero preserves records indefinitely. Alerts and
// current/last-success snapshots are never removed by this policy.
type RetentionPolicy struct {
	HistoryDays int       `json:"history_days"`
	AuditDays   int       `json:"audit_days"`
	Generation  uint64    `json:"generation"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	UpdatedBy   string    `json:"updated_by,omitempty"`
}

type RetentionPreview struct {
	ID            string          `json:"id"`
	ExpiresAt     time.Time       `json:"expires_at"`
	Policy        RetentionPolicy `json:"policy"`
	HistoryBefore *time.Time      `json:"history_before,omitempty"`
	AuditBefore   *time.Time      `json:"audit_before,omitempty"`
	HistoryCount  int64           `json:"history_count"`
	AuditCount    int64           `json:"audit_count"`
}

type retentionProgress struct {
	HistoryAfter    string    `json:"history_after"`
	AuditAfter      string    `json:"audit_after"`
	OperationsAfter string    `json:"operations_after"`
	LastRunAt       time.Time `json:"last_run_at"`
	HistoryRemoved  int64     `json:"history_removed"`
	AuditRemoved    int64     `json:"audit_removed"`
}

func readRetention(tx storage.Tx) (RetentionPolicy, error) {
	var policy RetentionPolicy
	err := tx.Get(retentionBucket, "policy", &policy)
	if errors.Is(err, storage.ErrNotFound) {
		err = nil
	}
	return policy, err
}

func (e *Engine) Retention(ctx context.Context) (RetentionPolicy, error) {
	var policy RetentionPolicy
	err := e.store.View(ctx, func(tx storage.Tx) error {
		var err error
		policy, err = readRetention(tx)
		return err
	})
	return policy, err
}

func validRetention(p RetentionPolicy) bool {
	return p.HistoryDays >= 0 && p.HistoryDays <= 3650 && p.AuditDays >= 0 && p.AuditDays <= 3650
}

func retentionCutoff(now time.Time, days int) *time.Time {
	if days == 0 {
		return nil
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	return &cutoff
}

func expiredCheck(c Check, cutoff *time.Time) bool {
	return cutoff != nil && !c.CompletedAt.IsZero() && c.CompletedAt.Before(*cutoff) && c.Status != StatusQueued && c.Status != StatusRunning
}

// PreviewRetention counts in read-only pages, avoiding a long global write
// transaction. Counts describe the preview time; ongoing collection may change
// them before confirmation. Confirmation authorizes the prospective policy too.
func (e *Engine) PreviewRetention(ctx context.Context, actor string, requested RetentionPolicy) (RetentionPreview, error) {
	if actor != "admin" || !validRetention(requested) {
		return RetentionPreview{}, ErrValidation
	}
	current, err := e.Retention(ctx)
	if err != nil {
		return RetentionPreview{}, err
	}
	if requested.Generation != current.Generation {
		return RetentionPreview{}, ErrConflict
	}
	id, err := randomID()
	if err != nil {
		return RetentionPreview{}, err
	}
	now := e.now()
	preview := RetentionPreview{ID: id, ExpiresAt: now.Add(5 * time.Minute), Policy: RetentionPolicy{HistoryDays: requested.HistoryDays, AuditDays: requested.AuditDays, Generation: current.Generation}, HistoryBefore: retentionCutoff(now, requested.HistoryDays), AuditBefore: retentionCutoff(now, requested.AuditDays)}
	for _, bucket := range []string{historyBucket, auditBucket} {
		if bucket == historyBucket && preview.HistoryBefore == nil || bucket == auditBucket && preview.AuditBefore == nil {
			continue
		}
		after := ""
		for {
			var rows []storage.Record
			err := e.store.View(ctx, func(tx storage.Tx) error {
				var err error
				rows, err = tx.Scan(bucket, storage.Scan{After: after, Limit: 500})
				return err
			})
			if err != nil {
				return RetentionPreview{}, err
			}
			for _, row := range rows {
				if bucket == historyBucket {
					var c Check
					if err := json.Unmarshal(row.Value, &c); err != nil {
						return RetentionPreview{}, err
					}
					if expiredCheck(c, preview.HistoryBefore) {
						preview.HistoryCount++
					}
				} else {
					var a storage.AuditRecord
					if err := json.Unmarshal(row.Value, &a); err != nil {
						return RetentionPreview{}, err
					}
					if !a.At.IsZero() && a.At.Before(*preview.AuditBefore) {
						preview.AuditCount++
					}
				}
				after = row.Key
			}
			if len(rows) < 500 {
				break
			}
		}
	}
	err = e.store.Update(ctx, func(tx storage.Tx) error {
		policy, err := readRetention(tx)
		if err != nil {
			return err
		}
		if policy.Generation != current.Generation {
			return ErrConflict
		}
		// One expiring preview per administrator; repeated previews cannot grow
		// an unbounded collection of abandoned authorization records.
		return tx.Put(retentionBucket, "preview/"+actor, preview)
	})
	return preview, err
}

func (e *Engine) ConfirmRetention(ctx context.Context, actor, previewID string) (RetentionPolicy, error) {
	if actor != "admin" || previewID == "" {
		return RetentionPolicy{}, ErrValidation
	}
	var policy RetentionPolicy
	err := e.store.Update(ctx, func(tx storage.Tx) error {
		var preview RetentionPreview
		if err := tx.Get(retentionBucket, "preview/"+actor, &preview); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return ErrConflict
			}
			return err
		}
		if preview.ID != previewID || !e.now().Before(preview.ExpiresAt) || !validRetention(preview.Policy) {
			return ErrConflict
		}
		current, err := readRetention(tx)
		if err != nil {
			return err
		}
		if current.Generation != preview.Policy.Generation {
			return ErrConflict
		}
		policy = preview.Policy
		policy.Generation++
		policy.UpdatedAt = e.now()
		policy.UpdatedBy = actor
		if err := tx.Put(retentionBucket, "policy", policy); err != nil {
			return err
		}
		if err := tx.Delete(retentionBucket, "preview/"+actor); err != nil {
			return err
		}
		if err := tx.Delete(retentionBucket, "progress"); err != nil {
			return err
		}
		return e.audit(tx, actor, retentionBucket, "retention.policy_set", e.now())
	})
	return policy, err
}

// PruneRetention inspects at most 200 records per bucket per invocation. Resume
// positions are committed with deletions so restart and transaction failure are
// safe. A sweep eventually wraps to revisit records which expired afterwards.
func (e *Engine) PruneRetention(ctx context.Context) error {
	return e.store.Update(ctx, func(tx storage.Tx) error {
		policy, err := readRetention(tx)
		if err != nil || policy.HistoryDays == 0 && policy.AuditDays == 0 {
			return err
		}
		var progress retentionProgress
		if err := tx.Get(retentionBucket, "progress", &progress); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		now := e.now()
		historyCutoff, auditCutoff := retentionCutoff(now, policy.HistoryDays), retentionCutoff(now, policy.AuditDays)
		if historyCutoff != nil {
			rows, err := tx.Scan(historyBucket, storage.Scan{After: progress.HistoryAfter, Limit: retentionBatchSize})
			if err != nil {
				return err
			}
			for _, row := range rows {
				progress.HistoryAfter = row.Key
				var c Check
				if err := json.Unmarshal(row.Value, &c); err != nil {
					return err
				}
				if !expiredCheck(c, historyCutoff) {
					continue
				}
				var p pending
				if err := tx.Get(queueBucket, c.Cursor, &p); err == nil {
					continue
				} else if !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				if err := tx.Delete(historyBucket, row.Key); err != nil {
					return err
				}
				if err := tx.Delete(checkIndexBucket, c.ServiceID+"/"+c.ID); err != nil {
					return err
				}
				progress.HistoryRemoved++
			}
			if len(rows) < retentionBatchSize {
				progress.HistoryAfter = ""
			}
			// Idempotency handles must not become dangling records. They expire
			// only after their observation is gone; queued/running work survives.
			rows, err = tx.Scan(operationBucket, storage.Scan{After: progress.OperationsAfter, Limit: retentionBatchSize})
			if err != nil {
				return err
			}
			for _, row := range rows {
				progress.OperationsAfter = row.Key
				var c Check
				if err := json.Unmarshal(row.Value, &c); err != nil {
					return err
				}
				if c.QueuedAt.IsZero() || !c.QueuedAt.Before(*historyCutoff) {
					continue
				}
				var existing Check
				if err := tx.Get(historyBucket, historyKey(c.ServiceID, c.Cursor), &existing); err == nil {
					continue
				} else if !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				var p pending
				if err := tx.Get(queueBucket, c.Cursor, &p); err == nil {
					continue
				} else if !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				if err := tx.Delete(operationBucket, row.Key); err != nil {
					return err
				}
			}
			if len(rows) < retentionBatchSize {
				progress.OperationsAfter = ""
			}
		}
		if auditCutoff != nil {
			rows, err := tx.Scan(auditBucket, storage.Scan{After: progress.AuditAfter, Limit: retentionBatchSize})
			if err != nil {
				return err
			}
			for _, row := range rows {
				progress.AuditAfter = row.Key
				var a storage.AuditRecord
				if err := json.Unmarshal(row.Value, &a); err != nil {
					return err
				}
				if !a.At.IsZero() && a.At.Before(*auditCutoff) {
					if err := tx.Delete(auditBucket, row.Key); err != nil {
						return err
					}
					progress.AuditRemoved++
				}
			}
			if len(rows) < retentionBatchSize {
				progress.AuditAfter = ""
			}
		}
		progress.LastRunAt = now
		return tx.Put(retentionBucket, "progress", progress)
	})
}
