package monitor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"relayservice/internal/storage"
)

var (
	ErrValidation = errors.New("invalid monitor input")
	ErrConflict   = errors.New("monitor configuration changed")
	ErrPaused     = errors.New("monitor is paused")
	ErrNotFound   = storage.ErrNotFound
)

const (
	servicesBucket   = "monitor.services"
	stateBucket      = "monitor.state"
	queueBucket      = "monitor.queue"
	activeBucket     = "monitor.active"
	historyBucket    = "monitor.history"
	checkIndexBucket = "monitor.check_index"
	operationBucket  = "monitor.operations"
	alertsBucket     = "monitor.alerts"
	silenceBucket    = "monitor.silence"
	ackBucket        = "monitor.ack"
	auditBucket      = "monitor.audit"
)

type serviceRecord struct {
	Service Service   `json:"service"`
	Cipher  string    `json:"cipher,omitempty"`
	NextDue time.Time `json:"next_due"`
}

type Engine struct {
	store   storage.Store
	crypt   Cryptor
	now     func() time.Time
	runMu   sync.Mutex
	running bool
}

func New(store storage.Store, crypt Cryptor) *Engine {
	return &Engine{store: store, crypt: crypt, now: func() time.Time { return time.Now().UTC() }}
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func pageLimit(n int) int {
	if n <= 0 {
		return 100
	}
	if n > 500 {
		return 500
	}
	return n
}
func historyKey(serviceID, cursor string) string { return serviceID + "/" + cursor }
func sequenceKey(n uint64) string                { return fmt.Sprintf("%020d", n) }
func operationKey(id, op string) string {
	h := sha256.Sum256([]byte(op))
	return id + "/" + hex.EncodeToString(h[:])
}

func validateInput(in *ServiceInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.UIURL = strings.TrimSpace(in.UIURL)
	if len(in.Name) == 0 || len(in.Name) > 120 || len(in.Group) > 120 || len(in.Token) > 8192 {
		return ErrValidation
	}
	switch in.Kind {
	case KindMihomo, KindOpenAI, KindStatuspage, KindLiteLLM, KindHermes, KindWorkbuddy2API, KindOpenWebUI, KindOllama, KindMetaCubeXD, KindHTTP, KindTCP:
	default:
		return ErrValidation
	}
	if in.PublicStatus != nil {
		if in.Kind != KindStatuspage || ValidatePublicStatusConfig(in.PublicStatus) != nil || in.Token != "" {
			return ErrValidation
		}
	}
	if (in.CollectModels || in.CollectLogs) && in.Kind != KindLiteLLM && in.Kind != KindWorkbuddy2API {
		return ErrValidation
	}
	if in.CollectLogs && in.Kind != KindLiteLLM {
		return ErrValidation
	}
	if in.Kind == KindStatuspage && in.Token != "" {
		return ErrValidation
	}
	if _, err := validatedURL(in.BaseURL); err != nil {
		return ErrValidation
	}
	if in.UIURL != "" {
		u, err := url.Parse(in.UIURL)
		if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || len(in.UIURL) > 2048 {
			return ErrValidation
		}
	}
	if strings.ContainsAny(in.Token, "\r\n") {
		return ErrValidation
	}
	if in.ClearToken && in.Token != "" {
		return ErrValidation
	}
	if in.IntervalSeconds == 0 {
		in.IntervalSeconds = 60
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = 5
	}
	if in.FailureThreshold == 0 {
		in.FailureThreshold = 3
	}
	if in.RecoveryThreshold == 0 {
		in.RecoveryThreshold = 2
	}
	if in.IntervalSeconds < 10 || in.IntervalSeconds > 86400 || in.TimeoutSeconds < 1 || in.TimeoutSeconds > 30 || in.TimeoutSeconds >= in.IntervalSeconds || in.FailureThreshold < 1 || in.FailureThreshold > 20 || in.RecoveryThreshold < 1 || in.RecoveryThreshold > 20 {
		return ErrValidation
	}
	return nil
}
func validatedURL(raw string) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, ErrValidation
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return nil, ErrValidation
	}
	// Connection URLs are origins, never arbitrary inference paths.
	if u.Path != "" && u.Path != "/" {
		return nil, ErrValidation
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, ErrValidation
		}
	}
	return u, nil
}

func (e *Engine) Save(ctx context.Context, actor string, in ServiceInput) (Service, error) {
	if err := validateInput(&in); err != nil {
		return Service{}, err
	}
	id := in.ID
	if id == "" {
		var err error
		id, err = randomID()
		if err != nil {
			return Service{}, err
		}
	}
	now := e.now()
	var out Service
	err := e.store.Update(ctx, func(tx storage.Tx) error {
		var rec serviceRecord
		err := tx.Get(servicesBucket, id, &rec)
		if in.ID == "" {
			if err == nil {
				return ErrConflict
			}
			if !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			rec.Service.CreatedAt = now
			rec.Service.Generation = 1
		} else {
			if err != nil {
				return err
			}
			if rec.Service.Tombstone {
				return ErrNotFound
			}
			if in.Generation != rec.Service.Generation {
				return ErrConflict
			}
			rec.Service.Generation++
		}
		cipher := rec.Cipher
		// Editing the connection must not forward an existing credential to a new target.
		if cipher != "" && rec.Service.BaseURL != in.BaseURL && in.Token == "" && !in.ClearToken {
			return ErrValidation
		}
		monitorChanged := !reflect.DeepEqual(rec.Service.PublicStatus, in.PublicStatus) || rec.Service.CollectModels != in.CollectModels || rec.Service.CollectLogs != in.CollectLogs || in.ID == "" || rec.Service.Kind != in.Kind || rec.Service.BaseURL != in.BaseURL || rec.Service.Enabled != in.Enabled || rec.Service.AllowPrivate != in.AllowPrivate || in.Token != "" || in.ClearToken
		if in.ClearToken {
			cipher = ""
		}
		if in.Token != "" {
			if e.crypt == nil {
				return ErrValidation
			}
			var err error
			if scoped, ok := e.crypt.(interface {
				EncryptFor(string, string, string) (string, error)
			}); ok {
				cipher, err = scoped.EncryptFor("monitor.service", id, in.Token)
			} else {
				cipher, err = e.crypt.Encrypt(in.Token)
			}
			if err != nil {
				return errors.New("monitor credential could not be encrypted")
			}
		}
		rec.Service = Service{PublicStatus: in.PublicStatus, CollectModels: in.CollectModels, CollectLogs: in.CollectLogs, ID: id, Name: in.Name, Kind: in.Kind, BaseURL: in.BaseURL, UIURL: in.UIURL, Group: in.Group, Enabled: in.Enabled, IntervalSeconds: in.IntervalSeconds, TimeoutSeconds: in.TimeoutSeconds, FailureThreshold: in.FailureThreshold, RecoveryThreshold: in.RecoveryThreshold, AllowPrivate: in.AllowPrivate, Generation: rec.Service.Generation, CreatedAt: rec.Service.CreatedAt, UpdatedAt: now}
		rec.Cipher = cipher
		rec.NextDue = now.Add(time.Duration(in.IntervalSeconds) * time.Second)
		if err := tx.Put(servicesBucket, id, rec); err != nil {
			return err
		}
		// A new configuration does not inherit health or incident counters from a previous target.
		if monitorChanged {
			if err := tx.Delete(stateBucket, id); err != nil {
				return err
			}
		}
		if err := e.audit(tx, actor, id, "service.save", now); err != nil {
			return err
		}
		out, err = e.serviceWithState(tx, rec)
		return err
	})
	return out, err
}

func (e *Engine) Remove(ctx context.Context, actor, id string, generation int64) error {
	return e.store.Update(ctx, func(tx storage.Tx) error {
		var rec serviceRecord
		if err := tx.Get(servicesBucket, id, &rec); err != nil {
			return err
		}
		if rec.Service.Tombstone {
			return ErrNotFound
		}
		if rec.Service.Generation != generation {
			return ErrConflict
		}
		rec.Service.Generation++
		rec.Service.Enabled = false
		rec.Service.Tombstone = true
		rec.Service.UpdatedAt = e.now()
		if err := tx.Put(servicesBucket, id, rec); err != nil {
			return err
		}
		return e.audit(tx, actor, id, "service.remove", e.now())
	})
}
func (e *Engine) audit(tx storage.Tx, actor, target, action string, now time.Time) error {
	return storage.AppendAudit(tx, storage.AuditRecord{ActorID: actor, TargetID: target, Action: action, At: now})
}

func (e *Engine) serviceWithState(tx storage.Tx, rec serviceRecord) (Service, error) {
	s := rec.Service
	var st state
	if err := tx.Get(stateBucket, s.ID, &st); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return Service{}, err
	}
	s.Current = st.Current
	s.LastSuccess = st.LastSuccess
	s.LastCheckAt = st.LastCheckAt
	s.LastSuccessAt = st.LastSuccessAt
	s.FailureCount = st.FailureCount
	s.RecoveryCount = st.RecoveryCount
	s.OpenIncident = st.OpenIncident
	s.OpenRestriction = st.OpenRestriction
	s.CredentialConfigured = rec.Cipher != ""
	s.Status = s.CurrentStatus()
	s.FreshnessState = s.Freshness(e.now())
	if s.Enabled && s.FreshnessState == "stale" {
		s.Status = StatusUnknown
	}
	s.EvidenceLevel = "process"
	if s.Kind == KindHTTP || s.Kind == KindTCP {
		s.EvidenceLevel = "connectivity_only"
	}
	if s.Kind == KindOpenAI {
		s.EvidenceLevel = "model_catalog"
	}
	if s.Kind == KindMihomo {
		s.EvidenceLevel = "subscription_metadata"
	}
	if s.Kind == KindStatuspage {
		s.EvidenceLevel = "provider_reported"
	}
	// Capability declarations for the basic-check slice, never probe results.
	// See docs/COMPATIBILITY.md; no inference or registry request is performed.
	s.InferenceState = "not_tested"
	s.UpdateState = "update_status_unavailable"
	return s, nil
}
func (e *Engine) Get(ctx context.Context, id string) (Service, error) {
	var out Service
	err := e.store.View(ctx, func(tx storage.Tx) error {
		var rec serviceRecord
		if err := tx.Get(servicesBucket, id, &rec); err != nil {
			return err
		}
		if rec.Service.Tombstone {
			return ErrNotFound
		}
		var err error
		out, err = e.serviceWithState(tx, rec)
		return err
	})
	return out, err
}
func (e *Engine) Services(ctx context.Context) ([]Service, error) {
	out := []Service{}
	err := e.store.View(ctx, func(tx storage.Tx) error {
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
				if !rec.Service.Tombstone {
					s, err := e.serviceWithState(tx, rec)
					if err != nil {
						return err
					}
					out = append(out, s)
				}
				after = row.Key
			}
			if len(rows) < 500 {
				break
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, err
}

func (e *Engine) RequestCheck(ctx context.Context, id, operationID string, generation int64) (Check, error) {
	if strings.TrimSpace(operationID) == "" || len(operationID) > 128 {
		return Check{}, ErrValidation
	}
	var out Check
	err := e.store.Update(ctx, func(tx storage.Tx) error {
		var rec serviceRecord
		if err := tx.Get(servicesBucket, id, &rec); err != nil {
			return err
		}
		if rec.Service.Tombstone {
			return ErrNotFound
		}
		if rec.Service.Generation != generation {
			return ErrConflict
		}
		if !rec.Service.Enabled {
			return ErrPaused
		}
		key := operationKey(id, operationID)
		if err := tx.Get(operationBucket, key, &out); err == nil {
			if out.Generation != generation {
				return ErrConflict
			}
			if err := tx.Get(historyBucket, historyKey(id, out.Cursor), &out); err == nil {
				return nil
			} else if !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			// The idempotency window ends with the retained observation, even
			// when the background sweep has not reached this handle yet. Never
			// replace work which is still queued or running.
			var p pending
			if err := tx.Get(queueBucket, out.Cursor, &p); err == nil {
				return ErrConflict
			} else if !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			if err := tx.Delete(operationBucket, key); err != nil {
				return err
			}
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		var err error
		out, err = e.enqueue(tx, rec)
		if err != nil {
			return err
		}
		if out.Generation != generation {
			return ErrConflict
		}
		return tx.Put(operationBucket, key, out)
	})
	return out, err
}
func (e *Engine) enqueue(tx storage.Tx, rec serviceRecord) (Check, error) {
	var active Check
	if err := tx.Get(activeBucket, rec.Service.ID, &active); err == nil {
		// One check per target stays in flight across configuration generations.
		// Completion of an old generation only updates history.
		return active, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return Check{}, err
	}
	id, err := randomID()
	if err != nil {
		return Check{}, err
	}
	n, err := tx.Next("monitor.check")
	if err != nil {
		return Check{}, err
	}
	c := Check{ID: id, Cursor: sequenceKey(n), ServiceID: rec.Service.ID, Kind: rec.Service.Kind, Generation: rec.Service.Generation, AdapterVersion: "monitor-v1", Status: StatusQueued, QueuedAt: e.now()}
	if err := tx.Put(queueBucket, c.Cursor, pending{Check: c}); err != nil {
		return Check{}, err
	}
	if err := tx.Put(activeBucket, c.ServiceID, c); err != nil {
		return Check{}, err
	}
	if err := tx.Put(historyBucket, historyKey(c.ServiceID, c.Cursor), c); err != nil {
		return Check{}, err
	}
	if err := tx.Put(checkIndexBucket, c.ServiceID+"/"+c.ID, c.Cursor); err != nil {
		return Check{}, err
	}
	return c, nil
}

// Check returns the persisted observation without running an external request.
func (e *Engine) Check(ctx context.Context, serviceID, checkID string) (Check, error) {
	var out Check
	err := e.store.View(ctx, func(tx storage.Tx) error {
		var cursor string
		if err := tx.Get(checkIndexBucket, serviceID+"/"+checkID, &cursor); err != nil {
			return err
		}
		return tx.Get(historyBucket, historyKey(serviceID, cursor), &out)
	})
	return out, err
}
func (e *Engine) History(ctx context.Context, id, cursor string, limit int) ([]Check, error) {
	if cursor != "" {
		if _, err := strconv.ParseUint(cursor, 10, 64); err != nil {
			return nil, ErrValidation
		}
		cursor = sequenceKeyMust(cursor)
	}
	out := []Check{}
	err := e.store.View(ctx, func(tx storage.Tx) error {
		after := ""
		if cursor != "" {
			after = historyKey(id, cursor)
		}
		rows, err := tx.Scan(historyBucket, storage.Scan{Prefix: id + "/", After: after, Reverse: true, Limit: pageLimit(limit)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			var c Check
			if err := json.Unmarshal(row.Value, &c); err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	return out, err
}
func sequenceKeyMust(s string) string { n, _ := strconv.ParseUint(s, 10, 64); return sequenceKey(n) }
func (e *Engine) Alerts(ctx context.Context, cursor string, limit int) ([]Alert, error) {
	after := ""
	if cursor != "" {
		n, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return nil, ErrValidation
		}
		after = sequenceKey(n)
	}
	out := []Alert{}
	err := e.store.View(ctx, func(tx storage.Tx) error {
		policy, err := readRetention(tx)
		if err != nil {
			return err
		}
		historyCutoff := retentionCutoff(e.now(), policy.HistoryDays)
		rows, err := tx.Scan(alertsBucket, storage.Scan{After: after, Limit: pageLimit(limit)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			var a Alert
			if err := json.Unmarshal(row.Value, &a); err != nil {
				return err
			}
			a.EvidenceState = "unavailable"
			var checkCursor string
			if err := tx.Get(checkIndexBucket, a.ServiceID+"/"+a.CheckID, &checkCursor); err == nil {
				a.EvidenceState = "available"
			} else if !errors.Is(err, storage.ErrNotFound) {
				return err
			} else if historyCutoff != nil && !a.StartedAt.IsZero() && a.StartedAt.Before(*historyCutoff) {
				a.EvidenceState = "expired"
			}
			var until time.Time
			for _, scope := range []string{"*", a.ServiceID} {
				var value silence
				if err := tx.Get(silenceBucket, scope, &value); err != nil && !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				if value.Until.After(until) {
					until = value.Until
				}
			}
			if until.After(e.now()) {
				a.Silenced = true
				a.SilenceUntil = &until
			}
			out = append(out, a)
		}
		return nil
	})
	return out, err
}

// Ack advances only this principal's read position. A batch must enumerate the
// actual alerts observed in order, starting with the next unread stored alert;
// a cursor alone acknowledges only the next alert. Numeric sequence gaps are
// allowed, but skipping an unread stored event is not.
func (e *Engine) Ack(ctx context.Context, deviceID string, cursor uint64, observed ...uint64) error {
	if deviceID == "" || len(observed) > 500 {
		return ErrValidation
	}
	if len(observed) == 0 {
		observed = []uint64{cursor}
	} else {
		if observed[len(observed)-1] != cursor {
			return ErrValidation
		}
		for i, value := range observed {
			if value == 0 || (i > 0 && value <= observed[i-1]) {
				return ErrValidation
			}
		}
	}
	return e.store.Update(ctx, func(tx storage.Tx) error {
		var old uint64
		if err := tx.Get(ackBucket, deviceID, &old); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if cursor < old {
			return ErrConflict
		}
		if cursor == old {
			return nil
		}
		// Concurrent requests may have acknowledged a prefix of the same page.
		// Validate only the remaining suffix against the persisted event order.
		first := 0
		for first < len(observed) && observed[first] <= old {
			var prior Alert
			if err := tx.Get(alertsBucket, sequenceKey(observed[first]), &prior); err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					return ErrValidation
				}
				return err
			}
			if prior.Cursor != observed[first] {
				return ErrValidation
			}
			first++
		}
		unread := observed[first:]
		rows, err := tx.Scan(alertsBucket, storage.Scan{After: sequenceKey(old), Limit: len(unread)})
		if err != nil {
			return err
		}
		if len(rows) != len(unread) {
			return ErrValidation
		}
		for i, row := range rows {
			if row.Key != sequenceKey(unread[i]) {
				return ErrValidation
			}
			var alert Alert
			if err := json.Unmarshal(row.Value, &alert); err != nil {
				return err
			}
			if alert.Cursor != unread[i] {
				return ErrValidation
			}
		}
		return tx.Put(ackBucket, deviceID, cursor)
	})
}

func (e *Engine) AckCursor(ctx context.Context, deviceID string) (uint64, error) {
	if deviceID == "" {
		return 0, ErrValidation
	}
	var cursor uint64
	err := e.store.View(ctx, func(tx storage.Tx) error {
		err := tx.Get(ackBucket, deviceID, &cursor)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		return err
	})
	return cursor, err
}
func (e *Engine) Silence(ctx context.Context, actor, serviceID string, until time.Time, reason string) error {
	now := e.now()
	if strings.TrimSpace(reason) == "" || len(reason) > 240 || (!until.IsZero() && (until.Before(now) || until.After(now.Add(30*24*time.Hour)))) {
		return ErrValidation
	}
	return e.store.Update(ctx, func(tx storage.Tx) error {
		scope := serviceID
		if scope == "" {
			scope = "*"
		}
		if serviceID != "" {
			var rec serviceRecord
			if err := tx.Get(servicesBucket, serviceID, &rec); err != nil {
				return err
			}
			if rec.Service.Tombstone {
				return ErrNotFound
			}
		}
		if until.IsZero() {
			if err := tx.Delete(silenceBucket, scope); err != nil {
				return err
			}
		} else {
			if err := tx.Put(silenceBucket, scope, silence{Until: until, Reason: reason}); err != nil {
				return err
			}
		}
		return e.audit(tx, actor, serviceID, "silence.set", now)
	})
}
func (e *Engine) Diagnostic(ctx context.Context) (any, error) {
	services, err := e.Services(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(services))
	for _, s := range services {
		items = append(items, map[string]any{"service_id": s.ID, "kind": s.Kind, "enabled": s.Enabled, "generation": s.Generation, "status": s.Status, "last_checked_at": s.LastCheckAt, "freshness": s.FreshnessState})
	}
	return map[string]any{"scope": "relay_monitoring", "generated_at": e.now(), "services": items, "inference": "not_enabled", "image_update": "update_status_unavailable"}, nil
}
func (s Service) CurrentStatus() string {
	if !s.Enabled {
		return "paused"
	}
	if s.Current == nil {
		return StatusUnknown
	}
	return s.Current.Status
}
func (s Service) Freshness(now time.Time) string {
	if s.LastCheckAt == nil {
		return "never_observed"
	}
	// D-05: stale only after more than two configured check intervals.
	// See docs/COMPATIBILITY.md for this basic-check freshness contract.
	if now.Sub(*s.LastCheckAt) > 2*time.Duration(s.IntervalSeconds)*time.Second {
		return "stale"
	}
	return "fresh"
}
