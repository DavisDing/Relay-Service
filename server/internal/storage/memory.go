package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is a transactional test/development store. Production uses PostgreSQL.
type Memory struct {
	mu        sync.RWMutex
	buckets   map[string]map[string]json.RawMessage
	sequences map[string]uint64
}

func NewMemory() *Memory {
	return &Memory{buckets: map[string]map[string]json.RawMessage{}, sequences: map[string]uint64{}}
}
func (*Memory) Ping(context.Context) error { return nil }
func (*Memory) Close()                     {}
func (m *Memory) DataVersion(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sequences["relay.data_version"], nil
}
func (m *Memory) View(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return fn(&memoryTx{ctx: ctx, buckets: m.buckets, sequences: m.sequences, readOnly: true})
}
func (m *Memory) Update(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	buckets := cloneBuckets(m.buckets)
	sequences := cloneSequences(m.sequences)
	tx := &memoryTx{ctx: ctx, buckets: buckets, sequences: sequences}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx.changed {
		sequences["relay.data_version"]++
	}
	m.buckets, m.sequences = buckets, sequences
	return nil
}
func cloneBuckets(source map[string]map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	out := make(map[string]map[string]json.RawMessage, len(source))
	for bucket, items := range source {
		copyItems := make(map[string]json.RawMessage, len(items))
		for key, value := range items {
			copyItems[key] = append(json.RawMessage(nil), value...)
		}
		out[bucket] = copyItems
	}
	return out
}
func cloneSequences(source map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

type memoryTx struct {
	ctx       context.Context
	buckets   map[string]map[string]json.RawMessage
	sequences map[string]uint64
	readOnly  bool
	changed   bool
}

func (t *memoryTx) Get(bucket, key string, target any) error {
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	value, ok := t.buckets[bucket][key]
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(value, target)
}
func (t *memoryTx) Put(bucket, key string, value any) error {
	if t.readOnly {
		return ErrReadOnly
	}
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if t.buckets[bucket] == nil {
		t.buckets[bucket] = map[string]json.RawMessage{}
	}
	t.buckets[bucket][key] = data
	t.changed = true
	return nil
}
func (t *memoryTx) Delete(bucket, key string) error {
	if t.readOnly {
		return ErrReadOnly
	}
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	delete(t.buckets[bucket], key)
	t.changed = true
	return nil
}
func (t *memoryTx) Scan(bucket string, options Scan) ([]Record, error) {
	if err := validIdentity(bucket, "scan"); err != nil {
		return nil, err
	}
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(t.buckets[bucket]))
	for key := range t.buckets[bucket] {
		if options.Prefix != "" && !strings.HasPrefix(key, options.Prefix) {
			continue
		}
		if options.After != "" && (!options.Reverse && key <= options.After || options.Reverse && key >= options.After) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if options.Reverse {
		for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
			keys[i], keys[j] = keys[j], keys[i]
		}
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]Record, 0, len(keys))
	for _, key := range keys {
		out = append(out, Record{Key: key, Value: append(json.RawMessage(nil), t.buckets[bucket][key]...)})
	}
	return out, nil
}

func (t *memoryTx) Count(bucket string, options CountOptions) (int64, error) {
	if err := validIdentity(bucket, "count"); err != nil {
		return 0, err
	}
	if options.TimeField == "" || options.Before.IsZero() {
		return 0, fmt.Errorf("count requires a timestamp field and cutoff")
	}
	if err := t.ctx.Err(); err != nil {
		return 0, err
	}
	var total int64
	for key, raw := range t.buckets[bucket] {
		if options.Prefix != "" && !strings.HasPrefix(key, options.Prefix) {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return 0, err
		}
		var at time.Time
		if value, ok := fields[options.TimeField]; !ok || json.Unmarshal(value, &at) != nil || at.IsZero() || !at.Before(options.Before) {
			continue
		}
		if options.StatusField != "" {
			var status string
			_ = json.Unmarshal(fields[options.StatusField], &status)
			excluded := false
			for _, candidate := range options.ExcludeStatuses {
				if status == candidate {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
		}
		total++
	}
	return total, nil
}
func (t *memoryTx) Next(sequence string) (uint64, error) {
	if t.readOnly {
		return 0, ErrReadOnly
	}
	if err := validIdentity(sequence, "sequence"); err != nil {
		return 0, err
	}
	if err := t.ctx.Err(); err != nil {
		return 0, err
	}
	t.sequences[sequence]++
	t.changed = true
	return t.sequences[sequence], nil
}
