package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"relayservice/internal/storage"
)

func TestPrepareSchema(t *testing.T) {
	failed := errors.New("database operation failed")
	for _, test := range []struct {
		name         string
		checks       []error
		migrationURL string
		openErr      error
		migrateErr   error
		wantErr      error
		events       []string
	}{
		{name: "initialized skips migration credentials", checks: []error{nil}, migrationURL: "setup-credential", events: []string{"check"}},
		{name: "initialized works without setup credentials", checks: []error{nil}, events: []string{"check"}},
		{name: "empty schema initializes before verification", checks: []error{storage.ErrSchemaNotReady, nil}, migrationURL: "setup-credential", events: []string{"check", "open", "migrate", "close", "check"}},
		{name: "single account initializes before verification", checks: []error{storage.ErrSchemaNotReady, nil}, migrationURL: "business-credential", events: []string{"check", "open", "migrate", "close", "check"}},
		{name: "pending schema wraps readiness error", checks: []error{fmt.Errorf("pending: %w", storage.ErrSchemaNotReady), nil}, migrationURL: "setup-credential", events: []string{"check", "open", "migrate", "close", "check"}},
		{name: "missing setup credentials stops startup", checks: []error{storage.ErrSchemaNotReady}, wantErr: storage.ErrSchemaNotReady, events: []string{"check"}},
		{name: "connectivity permission or incompatible history does not initialize", checks: []error{failed}, migrationURL: "setup-credential", wantErr: failed, events: []string{"check"}},
		{name: "initialization connection failure stops startup", checks: []error{storage.ErrSchemaNotReady}, migrationURL: "setup-credential", openErr: failed, wantErr: failed, events: []string{"check", "open"}},
		{name: "SQL failure closes setup pool and stops startup", checks: []error{storage.ErrSchemaNotReady}, migrationURL: "setup-credential", migrateErr: failed, wantErr: failed, events: []string{"check", "open", "migrate", "close"}},
		{name: "application role must verify initialized schema", checks: []error{storage.ErrSchemaNotReady, failed}, migrationURL: "setup-credential", wantErr: failed, events: []string{"check", "open", "migrate", "close", "check"}},
		{name: "unfinished initialization is not success", checks: []error{storage.ErrSchemaNotReady, storage.ErrSchemaNotReady}, migrationURL: "setup-credential", wantErr: storage.ErrSchemaNotReady, events: []string{"check", "open", "migrate", "close", "check"}},
		{name: "cancellation stops startup", checks: []error{context.Canceled}, migrationURL: "setup-credential", wantErr: context.Canceled, events: []string{"check"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var events []string
			app := &startupSchemaCheck{events: &events, checks: test.checks}
			ctx := context.WithValue(context.Background(), startupContextKey{}, test.name)
			err := prepareSchema(ctx, app, test.migrationURL, func(got context.Context, url string) (schemaMigrator, error) {
				events = append(events, "open")
				if got != ctx || url != test.migrationURL {
					t.Fatal("initialization did not receive the startup context and configured credential")
				}
				if test.openErr != nil {
					return nil, test.openErr
				}
				return &startupSchemaMigration{events: &events, err: test.migrateErr, ctx: ctx}, nil
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("prepareSchema error=%v; want %v", err, test.wantErr)
			}
			if !reflect.DeepEqual(events, test.events) {
				t.Fatalf("startup order=%v; want %v", events, test.events)
			}
		})
	}
}

type startupContextKey struct{}

type startupSchemaCheck struct {
	events *[]string
	checks []error
}

func (s *startupSchemaCheck) Ping(context.Context) error {
	*s.events = append(*s.events, "check")
	if len(s.checks) == 0 {
		return errors.New("unexpected schema check")
	}
	err := s.checks[0]
	s.checks = s.checks[1:]
	return err
}

type startupSchemaMigration struct {
	events *[]string
	err    error
	ctx    context.Context
}

func (s *startupSchemaMigration) Migrate(ctx context.Context) error {
	*s.events = append(*s.events, "migrate")
	if ctx != s.ctx {
		return errors.New("migration lost startup context")
	}
	return s.err
}
func (s *startupSchemaMigration) Close() { *s.events = append(*s.events, "close") }
