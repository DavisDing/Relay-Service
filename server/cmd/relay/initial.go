package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"relayservice/internal/access"
	"relayservice/internal/config"
)

func prepareInitialAdministrator(ctx context.Context, service *access.Service, cfg *config.Config, deploymentLog io.Writer) error {
	// Drop the configuration reference before serving HTTP, including on errors.
	// Go strings/GC do not provide guaranteed erasure of old memory copies.
	defer func() {
		cfg.InitialPassword = ""
		_ = os.Unsetenv("RELAY_INITIAL_PASSWORD")
	}()
	deploymentFile, passwordFile := cfg.DeploymentFile, cfg.InitialPasswordFile
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if err := config.RemoveInitialPassword(deploymentFile, passwordFile); err != nil {
				slog.Warn("initial credential file cleanup pending; retrying every 30 seconds", "error", err)
				go retryInitialPasswordCleanup(ctx, 30*time.Second, func() error {
					return config.RemoveInitialPassword(deploymentFile, passwordFile)
				})
			}
		})
	}
	service.SetInitialPasswordCleanup(cleanup)
	pending, err := service.EnsureInitialAdministrator(ctx, cfg.InitialPassword)
	if errors.Is(err, access.ErrInitialPasswordMismatch) || errors.Is(err, access.ErrInitialPasswordMissing) {
		slog.Warn("initial administrator credential unavailable; original account remains usable", "error", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("prepare initial administrator: %w", err)
	}
	if pending {
		// Deployment-only credential display; never expose it through HTTP.
		fmt.Fprintf(deploymentLog, "Relay first login — username: admin; temporary password: %s\nChange this password in the browser before using Relay.\n", cfg.InitialPassword)
	} else {
		initialized, err := service.Initialized(ctx)
		if err != nil {
			return fmt.Errorf("check administrator before initial password cleanup: %w", err)
		}
		if initialized {
			cleanup()
		}
	}
	return nil
}

// Only a committed password change or an already initialized account starts
// this worker. Its lifetime is the process context, not the HTTP request or the
// short-lived database connection context used during startup.
func retryInitialPasswordCleanup(ctx context.Context, interval time.Duration, cleanup func() error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			if err := cleanup(); err != nil {
				slog.Warn("initial credential file cleanup still pending", "error", err)
			} else {
				slog.Info("initial credential file cleanup completed")
				return
			}
		}
	}
}
