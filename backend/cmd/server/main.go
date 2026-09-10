// Command server is the home-finance-planner backend entrypoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"home-finance-planner/backend/internal/api"
	"home-finance-planner/backend/internal/config"
	"home-finance-planner/backend/internal/crypto"
	"home-finance-planner/backend/internal/extractor"
	"home-finance-planner/backend/internal/repository"
	"home-finance-planner/backend/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	healthcheck := flag.Bool("healthcheck", false, "verify the server is running (used by docker healthcheck)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := newLogger(cfg)

	if *healthcheck {
		return doHealthcheck(cfg, log)
	}

	ctx := context.Background()

	db, err := repository.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	accounts := repository.NewAccountRepository(db)
	categories := repository.NewCategoryRepository(db)
	stores := repository.NewStoreRepository(db)
	transactions := repository.NewTransactionRepository(db)
	budgets := repository.NewBudgetRepository(db)
	summary := repository.NewSummaryRepository(db)
	bills := repository.NewBillRepository(db)
	billScans := repository.NewBillScanRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)

	box, err := newEncryptionBox(cfg, log)
	if err != nil {
		return err
	}
	billExtractor := extractor.New(cfg.LLMTimeout)
	settingsSvc := service.NewSettingsService(settingsRepo, box, billExtractor)
	storeSvc := service.NewStoreService(stores, cfg.StoresPath)
	billSvc := service.NewBillService(bills, billScans, billExtractor, settingsSvc,
		accounts, categories, stores, budgets, transactions, cfg.BillsPath, cfg.LLMTimeout, log)

	svc := service.New(accounts, categories, storeSvc, transactions, budgets, summary, settingsSvc, billSvc)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      api.NewRouter(cfg, log, svc),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server starting",
			"addr", srv.Addr,
			"env", cfg.Env,
			"db", cfg.DBPath,
		)
		errCh <- srv.ListenAndServe()
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
	case sig := <-stop:
		log.Info("shutting down", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		// Stop the scan workers before the DB pool closes (never waits for a
		// running extraction — those scans resume on the next start).
		billSvc.Close()
	}
	return nil
}

// doHealthcheck exits 0 when the local server answers /api/v1/health.
func doHealthcheck(cfg config.Config, log *slog.Logger) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", cfg.Port))
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: unexpected status %d", resp.StatusCode)
	}
	log.Debug("healthcheck ok")
	return nil
}

// newEncryptionBox builds the AES box used to encrypt AI API keys at rest.
// Production requires AI_ENCRYPTION_KEY; development falls back to an
// insecure default with a warning.
func newEncryptionBox(cfg config.Config, log *slog.Logger) (*crypto.Box, error) {
	key := cfg.AIEncryptionKey
	if key == "" {
		if cfg.IsProduction() {
			return nil, fmt.Errorf("AI_ENCRYPTION_KEY must be set in production to store AI provider keys securely")
		}
		log.Warn("AI_ENCRYPTION_KEY is not set; using an insecure development key — AI provider API keys are NOT protected")
		key = "insecure-development-key"
	}
	return crypto.NewBox(key)
}

func newLogger(cfg config.Config) *slog.Logger {
	var handler slog.Handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	if cfg.IsProduction() {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	}
	return slog.New(handler)
}
