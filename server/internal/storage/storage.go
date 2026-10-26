// Package storage provides small transactional record operations backed by PostgreSQL.
// Buckets are owned by their business modules; only the relay database is touched.
package storage

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrNotFound = errors.New("record not found")
var ErrReadOnly = errors.New("read-only storage transaction")

type Record struct {
	Key   string
	Value json.RawMessage
}

type Scan struct {
	Prefix  string
	After   string
	Limit   int
	Reverse bool
}

type Tx interface {
	Get(bucket, key string, value any) error
	Put(bucket, key string, value any) error
	Delete(bucket, key string) error
	Scan(bucket string, options Scan) ([]Record, error)
	Next(sequence string) (uint64, error)
}

type Store interface {
	View(context.Context, func(Tx) error) error
	Update(context.Context, func(Tx) error) error
	Ping(context.Context) error
	Close()
}
