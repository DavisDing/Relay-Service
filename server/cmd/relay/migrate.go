package main

import (
	"context"
	"relayservice/internal/config"
	"relayservice/internal/storage"
	"time"
)

func migrate() error {
	databaseURL := config.DatabaseURLFromEnvironment()
	if err := config.ValidateDatabaseURL(databaseURL); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := storage.OpenPostgres(ctx, databaseURL, 4)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	return nil
}
