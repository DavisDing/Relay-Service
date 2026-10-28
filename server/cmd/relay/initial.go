package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

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
	cleanup := func() {
		if err := config.RemoveInitialPassword(deploymentFile, passwordFile); err != nil {
			slog.Warn("initial credential file cleanup pending; it will retry on restart", "error", err)
		}
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
