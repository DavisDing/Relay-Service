// Package storage provides small transactional record operations backed by PostgreSQL.
// Buckets are owned by their business modules; only the relay database is touched.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("record not found")
var ErrReadOnly = errors.New("read-only storage transaction")
var ErrSchemaNotReady = errors.New("database schema requires initialization")

type Record struct {
	Key   string
	Value json.RawMessage
}

type Scan struct {
	Prefix  string
	After   string
	Limit   int
	Reverse bool
	// Keys optionally restricts a scan to at most 500 exact record keys.
	Keys []string
}

// CountOptions describes a bounded timestamp count over JSON records. The
// storage layer owns the query shape so callers do not need to scan and
// deserialize every record in a large bucket just to prepare a preview.
type CountOptions struct {
	Prefix          string
	Before          time.Time
	TimeField       string
	StatusField     string
	ExcludeStatuses []string
}

type Tx interface {
	Get(bucket, key string, value any) error
	Put(bucket, key string, value any) error
	Delete(bucket, key string) error
	Scan(bucket string, options Scan) ([]Record, error)
	Count(bucket string, options CountOptions) (int64, error)
	Next(sequence string) (uint64, error)
}

type Store interface {
	View(context.Context, func(Tx) error) error
	Update(context.Context, func(Tx) error) error
	Ping(context.Context) error
	Close()
}

// Database is a persistent store with exclusive scheduler ownership.
type Database interface {
	Store
	DataVersion(context.Context) (uint64, error)
	AcquireLeadership(context.Context) (Leader, error)
}
