package monitor

import (
	"context"
	"time"

	"relayservice/internal/storage"
)

// Preview uses the scheduled adapter and its network policy without saving a
// service, history, incident or credential. Existing credentials stay bound to
// their saved origin, and stale edits are rejected before any network request.
func (e *Engine) Preview(ctx context.Context, in ServiceInput) (Check, error) {
	if err := validateInput(&in); err != nil {
		return Check{}, err
	}
	rec := serviceRecord{}
	if in.ID != "" {
		if err := e.store.View(ctx, func(tx storage.Tx) error { return tx.Get(servicesBucket, in.ID, &rec) }); err != nil {
			return Check{}, err
		}
		if rec.Service.Tombstone {
			return Check{}, ErrNotFound
		}
		if rec.Service.Generation != in.Generation {
			return Check{}, ErrConflict
		}
		if rec.Cipher != "" && rec.Service.BaseURL != in.BaseURL && in.Token == "" && !in.ClearToken {
			return Check{}, ErrValidation
		}
	}
	owner := in.ID
	if owner == "" {
		var err error
		owner, err = randomID()
		if err != nil {
			return Check{}, err
		}
	}
	rec.Service = Service{PublicStatus: in.PublicStatus, CollectModels: in.CollectModels, CollectLogs: in.CollectLogs, ID: owner, Kind: in.Kind, BaseURL: in.BaseURL, AllowPrivate: in.AllowPrivate, TimeoutSeconds: in.TimeoutSeconds}
	if in.ClearToken {
		rec.Cipher = ""
	}
	if in.Token != "" {
		if e.crypt == nil {
			return Check{}, ErrValidation
		}
		var err error
		if scoped, ok := e.crypt.(interface {
			EncryptFor(string, string, string) (string, error)
		}); ok {
			rec.Cipher, err = scoped.EncryptFor("monitor.service", owner, in.Token)
		} else {
			rec.Cipher, err = e.crypt.Encrypt(in.Token)
		}
		if err != nil {
			return Check{}, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	result := e.execute(ctx, rec)
	return Check{Kind: in.Kind, Status: result.status, Class: result.class, Message: result.message, Success: result.success, StartedAt: start.UTC(), CompletedAt: time.Now().UTC(), DurationMS: time.Since(start).Milliseconds(), Details: result.details}, nil
}
