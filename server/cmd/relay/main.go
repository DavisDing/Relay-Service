package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"relayservice/internal/access"
	"relayservice/internal/api"
	"relayservice/internal/config"
	"relayservice/internal/monitor"
	"relayservice/internal/storage"
)

var (
	version  = "dev"
	revision = "unknown"
	created  = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			if err := json.NewEncoder(os.Stdout).Encode(struct {
				Version  string `json:"version"`
				Revision string `json:"revision"`
				Created  string `json:"created"`
			}{version, revision, created}); err != nil {
				os.Exit(1)
			}
			return
		case "healthcheck":
			os.Exit(checkHealth())
		case "migrate":
			if err := migrate(); err != nil {
				slog.Error("Relay migration failed", "error", err)
				os.Exit(1)
			}
			fmt.Println("Relay schema migrations applied")
			return
		case "run":
		default:
			fmt.Fprintln(os.Stderr, "usage: relay [run|migrate|healthcheck|version]")
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		slog.Error("Relay exited", "error", err)
		os.Exit(1)
	}
}

func checkHealth() int {
	addr := strings.TrimSpace(os.Getenv("RELAY_LISTEN_ADDR"))
	if addr == "" {
		addr = ":8787"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/health/live")
	if err != nil {
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level(cfg.LogLevel)})))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := storage.OpenPostgres(connectCtx, cfg.DatabaseURL, 8)
	if err != nil {
		return fmt.Errorf("connect Relay database: %w", err)
	}
	defer db.Close()
	leader, err := db.AcquireLeadership(connectCtx)
	if err != nil {
		return fmt.Errorf("acquire scheduler leadership: %w", err)
	}
	defer leader.Close()
	if err := db.Ping(connectCtx); err != nil {
		return fmt.Errorf("check Relay schema; run the deployment migration command first: %w", err)
	}
	if cfg.BootstrapToken == "" {
		slog.Warn("administrator bootstrap is disabled")
	}
	accessSvc, err := access.New(db, cfg.EncryptionKey, cfg.BootstrapToken, cfg.SecureCookies)
	if err != nil {
		return err
	}
	engine := monitor.New(db, accessSvc)
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	handler := api.New(db, accessSvc, engine, api.Options{Version: version, SecureCookies: cfg.SecureCookies, AllowedOrigin: cfg.PublicURL})
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on configured address: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	results := make(chan componentResult, 3)
	go func() { results <- componentResult{name: "http", err: server.Serve(listener)} }()
	go func() { results <- componentResult{name: "monitor", err: engine.Run(workerCtx)} }()
	go func() { results <- componentResult{name: "leadership", err: leader.Watch(workerCtx)} }()
	slog.Info("Relay listening", "address", listener.Addr().String(), "version", version, "relay_id", safeRelayID(ctx, accessSvc))

	var cause error
	first := <-results
	if ctx.Err() == nil {
		if first.name == "http" && !errors.Is(first.err, http.ErrServerClosed) {
			cause = fmt.Errorf("HTTP server stopped: %w", first.err)
		} else if first.name == "leadership" || first.name == "monitor" {
			cause = fmt.Errorf("%s worker stopped: %w", first.name, first.err)
		}
	}
	workerCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if cause != nil || shutdownErr != nil {
		_ = server.Close()
	}
	for received := 1; received < 3; received++ {
		select {
		case result := <-results:
			if result.err != nil && !errors.Is(result.err, context.Canceled) && !errors.Is(result.err, http.ErrServerClosed) && cause == nil {
				cause = fmt.Errorf("%s worker stopped: %w", result.name, result.err)
			}
		case <-shutdownCtx.Done():
			if shutdownErr == nil {
				shutdownErr = shutdownCtx.Err()
			}
			received = 3
		}
	}
	if shutdownErr != nil {
		return shutdownErr
	}
	return cause
}

type componentResult struct {
	name string
	err  error
}

func safeRelayID(ctx context.Context, service *access.Service) string {
	id, err := service.RelayID(ctx)
	if err != nil {
		return "unknown"
	}
	return id
}

func level(raw string) slog.Level {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
