package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"relayservice/internal/storage"
)

// Run owns the single local scheduler. The process-level PostgreSQL lock is held
// by the application; persisted queued work is claimed before any network I/O.
func (e *Engine) Run(ctx context.Context) error {
	e.runMu.Lock()
	if e.running {
		e.runMu.Unlock()
		return ErrConflict
	}
	e.running = true
	e.runMu.Unlock()
	defer func() { e.runMu.Lock(); e.running = false; e.runMu.Unlock() }()
	if err := e.recoverInterrupted(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Exactly one result is sent. The one-slot buffer also accepts it if
	// cancellation ends the receive loop before PruneRetention returns.
	startupCleanup := make(chan error, 1)
	go func() { startupCleanup <- e.PruneRetention(ctx) }()
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := e.RunNetwork(ctx); err != nil {
			select {
			case failures <- err:
			default:
			}
			cancel()
		}
	}()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.worker(ctx); err != nil {
				select {
				case failures <- err:
				default:
				}
				cancel()
			}
		}()
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	cleanup := time.NewTicker(30 * time.Second)
	defer cleanup.Stop()
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-startupCleanup:
			startupCleanup = nil
			if err != nil {
				runErr = err
				break loop
			}
		case err := <-failures:
			runErr = err
			break loop
		case <-cleanup.C:
			if err := e.PruneRetention(ctx); err != nil {
				runErr = err
				break loop
			}
		case <-ticker.C:
			if err := e.schedule(ctx); err != nil {
				runErr = err
				break loop
			}
		}
	}
	cancel()
	wg.Wait()
	if runErr != nil {
		return runErr
	}
	select {
	case err := <-failures:
		return err
	default:
		return nil
	}
}

func (e *Engine) schedule(ctx context.Context) error {
	now := e.now()
	return e.store.Update(ctx, func(tx storage.Tx) error {
		after := ""
		for {
			rows, err := tx.Scan(servicesBucket, storage.Scan{After: after, Limit: 500})
			if err != nil {
				return err
			}
			for _, row := range rows {
				var rec serviceRecord
				if err := json.Unmarshal(row.Value, &rec); err != nil {
					return err
				}
				after = row.Key
				if rec.Service.Enabled && !rec.Service.Tombstone && !rec.NextDue.After(now) {
					if _, err := e.enqueue(tx, rec); err != nil {
						return err
					}
					rec.NextDue = now.Add(time.Duration(rec.Service.IntervalSeconds) * time.Second)
					if err := tx.Put(servicesBucket, row.Key, rec); err != nil {
						return err
					}
				}
			}
			if len(rows) < 500 {
				break
			}
		}
		return nil
	})
}
func (e *Engine) recoverInterrupted(ctx context.Context) error {
	return e.store.Update(ctx, func(tx storage.Tx) error {
		after := ""
		for {
			rows, err := tx.Scan(queueBucket, storage.Scan{After: after, Limit: 500})
			if err != nil {
				return err
			}
			for _, row := range rows {
				after = row.Key
				var p pending
				if err := json.Unmarshal(row.Value, &p); err != nil {
					return err
				}
				if p.Check.Status == StatusRunning {
					p.Check.Status = StatusInterrupted
					p.Check.Class = "process_interrupted"
					p.Check.Message = "Relay 重启导致检查中断。"
					p.Check.CompletedAt = e.now()
					p.Check.DurationMS = elapsedMilliseconds(p.Check.StartedAt, p.Check.CompletedAt)
					if err := e.closePending(tx, p.Check); err != nil {
						return err
					}
				}
			}
			if len(rows) < 500 {
				break
			}
		}
		return nil
	})
}
func (e *Engine) claim(ctx context.Context) (Check, serviceRecord, bool, error) {
	var c Check
	var rec serviceRecord
	found := false
	err := e.store.Update(ctx, func(tx storage.Tx) error {
		after := ""
		for {
			rows, err := tx.Scan(queueBucket, storage.Scan{After: after, Limit: 100})
			if err != nil {
				return err
			}
			for _, row := range rows {
				after = row.Key
				var p pending
				if err := json.Unmarshal(row.Value, &p); err != nil {
					return err
				}
				if p.Check.Status != StatusQueued {
					continue
				}
				err := tx.Get(servicesBucket, p.Check.ServiceID, &rec)
				if err != nil && !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				if err != nil || rec.Service.Tombstone || !rec.Service.Enabled || rec.Service.Generation != p.Check.Generation {
					p.Check.Status = StatusInterrupted
					p.Check.Class = "configuration_changed"
					p.Check.Message = "执行前配置已变更，此次检查已中断。"
					p.Check.CompletedAt = e.now()
					if err := e.closePending(tx, p.Check); err != nil {
						return err
					}
					continue
				}
				c = p.Check
				c.Status = StatusRunning
				c.StartedAt = e.now()
				if err := tx.Put(queueBucket, c.Cursor, pending{Check: c}); err != nil {
					return err
				}
				if err := tx.Put(historyBucket, historyKey(c.ServiceID, c.Cursor), c); err != nil {
					return err
				}
				if err := tx.Put(activeBucket, c.ServiceID, c); err != nil {
					return err
				}
				found = true
				return nil
			}
			if len(rows) < 100 {
				break
			}
		}
		return nil
	})
	return c, rec, found, err
}
func (e *Engine) closePending(tx storage.Tx, c Check) error {
	if err := tx.Put(historyBucket, historyKey(c.ServiceID, c.Cursor), c); err != nil {
		return err
	}
	if err := tx.Delete(queueBucket, c.Cursor); err != nil {
		return err
	}
	var active Check
	if err := tx.Get(activeBucket, c.ServiceID, &active); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if active.ID == c.ID {
		return tx.Delete(activeBucket, c.ServiceID)
	}
	return nil
}
func (e *Engine) worker(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		c, rec, found, err := e.claim(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !found {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			continue
		}
		result := e.execute(ctx, rec)
		c.CompletedAt = e.now()
		c.DurationMS = elapsedMilliseconds(c.StartedAt, c.CompletedAt)
		c.Success = result.success
		c.Status = result.status
		c.Class = result.class
		c.Message = result.message
		c.Details = result.details
		// Cancellation is an interrupted observation, never a failure attributed to the target.
		if ctx.Err() != nil {
			c.Success = false
			c.Status = StatusInterrupted
			c.Class = "process_interrupted"
			c.Message = "Relay 关闭导致检查中断。"
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = e.finish(finishCtx, c)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// A missing start is an unmeasured duration, and wall-clock corrections must
// never turn an observation's elapsed time negative.
func elapsedMilliseconds(start, end time.Time) int64 {
	if start.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}
func (e *Engine) finish(ctx context.Context, c Check) error {
	return e.store.Update(ctx, func(tx storage.Tx) error {
		var p pending
		if err := tx.Get(queueBucket, c.Cursor, &p); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return nil
			}
			return err
		}
		if p.Check.ID != c.ID {
			return ErrConflict
		}
		if err := e.closePending(tx, c); err != nil {
			return err
		}
		var rec serviceRecord
		if err := tx.Get(servicesBucket, c.ServiceID, &rec); err != nil {
			return err
		}
		// Old-generation outcomes are useful history, but never current target health.
		if rec.Service.Generation != c.Generation || rec.Service.Tombstone || !rec.Service.Enabled || c.Status == StatusInterrupted {
			return nil
		}
		var st state
		if err := tx.Get(stateBucket, c.ServiceID, &st); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		st.Current = &c
		st.LastCheckAt = &c.CompletedAt
		if c.Success {
			st.LastSuccess = &c
			st.LastSuccessAt = &c.CompletedAt
			if st.OpenIncident {
				st.RecoveryCount++
			} else {
				st.RecoveryCount = 0
			}
			st.FailureCount = 0
			if st.OpenRestriction {
				st.RestrictionCount++
				if st.RestrictionCount >= rec.Service.RecoveryThreshold {
					st.OpenRestriction = false
					st.RestrictionCount = 0
					incidentID := st.RestrictionID
					st.RestrictionID = ""
					if err := e.event(tx, c, "capability_recovered", "info", 1, incidentID); err != nil {
						return err
					}
				}
			} else {
				st.RestrictionCount = 0
			}
			if st.OpenIncident && st.RecoveryCount >= rec.Service.RecoveryThreshold {
				st.OpenIncident = false
				st.RecoveryCount = 0
				incidentID := st.IncidentID
				st.IncidentID = ""
				if err := e.event(tx, c, "recovery", "info", 1, incidentID); err != nil {
					return err
				}
			}
		} else if c.Status == StatusUnauthorized || c.Status == StatusUnsupported {
			st.FailureCount = 0
			st.RecoveryCount = 0
			st.RestrictionCount = 0
			if !st.OpenRestriction {
				st.OpenRestriction = true
				st.RestrictionID = c.ID
				if err := e.event(tx, c, "capability_restricted", "warning", 1, c.ID); err != nil {
					return err
				}
			}
		} else {
			st.FailureCount++
			st.RecoveryCount = 0
			st.RestrictionCount = 0
			if !st.OpenIncident && st.FailureCount >= rec.Service.FailureThreshold {
				st.OpenIncident = true
				st.IncidentID = c.ID
				if err := e.event(tx, c, "failure", "warning", st.FailureCount, c.ID); err != nil {
					return err
				}
			}
		}
		return tx.Put(stateBucket, c.ServiceID, st)
	})
}
func (e *Engine) event(tx storage.Tx, c Check, kind, severity string, count int, incidentID string) error {
	n, err := tx.Next("monitor.alert")
	if err != nil {
		return err
	}
	a := Alert{Cursor: n, ID: sequenceKey(n), ServiceID: c.ServiceID, CheckID: c.ID, Generation: c.Generation, IncidentID: incidentID, Kind: kind, Severity: severity, Message: c.Message, StartedAt: c.CompletedAt, Count: count}
	if kind == "recovery" || kind == "capability_recovered" {
		a.ResolvedAt = &c.CompletedAt
	}
	return tx.Put(alertsBucket, sequenceKey(n), a)
}
