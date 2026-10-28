package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"relayservice/internal/storage"
)

type schemaChecker interface {
	Ping(context.Context) error
}

type schemaMigrator interface {
	Migrate(context.Context) error
	Close()
}

// prepareSchema runs before opening the HTTP listener or starting workers.
// Already initialized databases need only the normal business connection.
func prepareSchema(ctx context.Context, app schemaChecker, migrationURL string, open func(context.Context, string) (schemaMigrator, error)) error {
	err := app.Ping(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, storage.ErrSchemaNotReady) {
		return fmt.Errorf("check Relay database schema: %w", err)
	}
	if migrationURL == "" {
		return fmt.Errorf("%w; configure RELAY_DATABASE_URL for automatic initialization", err)
	}
	slog.Info("initializing Relay database schema")
	db, err := open(ctx, migrationURL)
	if err != nil {
		return fmt.Errorf("connect schema initialization database: %w", err)
	}
	// Close the DDL-capable pool before normal service startup, on every path.
	err = func() error {
		defer db.Close()
		return db.Migrate(ctx)
	}()
	if err != nil {
		return fmt.Errorf("initialize Relay database schema: %w", err)
	}
	if err := app.Ping(ctx); err != nil {
		return fmt.Errorf("verify initialized Relay database with application role: %w", err)
	}
	slog.Info("Relay database schema ready")
	return nil
}
