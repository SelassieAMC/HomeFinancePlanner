// Command server is the home-finance-planner backend entrypoint.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/riverqueue/river"

	"home-finance-planner/backend/internal/api"
	"home-finance-planner/backend/internal/config"
	"home-finance-planner/backend/internal/crypto"
	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/extractor"
	"home-finance-planner/backend/internal/fx"
	"home-finance-planner/backend/internal/jobs"
	"home-finance-planner/backend/internal/repository"
	"home-finance-planner/backend/internal/service"

	"riverqueue.com/riverui"
)

// riverUIPrefix is where the embedded River web UI is mounted on the server.
const riverUIPrefix = "/riverui"

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
	products := repository.NewProductRepository(db)
	transactions := repository.NewTransactionRepository(db)
	budgets := repository.NewBudgetRepository(db)
	summary := repository.NewSummaryRepository(db)
	bills := repository.NewBillRepository(db)
	billScans := repository.NewBillScanRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	productMappings := repository.NewProductNameMappingRepository(db)
	insights := repository.NewInsightRepository(db)

	box, err := newEncryptionBox(cfg, log)
	if err != nil {
		return err
	}
	billExtractor := extractor.New(cfg.LLMTimeout, cfg.LLMNumCtx)
	fetcher := fx.New(cfg.FXTimeout)
	settingsSvc := service.NewSettingsService(settingsRepo, box, billExtractor)
	fxSvc := service.NewFXService(settingsRepo, fetcher, log)
	promptRepo := repository.NewAIPromptRepository(db)
	promptSvc := service.NewAIPromptService(promptRepo, categories)
	storeSvc := service.NewStoreService(stores, cfg.StoresPath)
	productSvc := service.NewProductService(products, categories, cfg.ProductsPath,
		productMappings, billExtractor, settingsSvc, promptSvc, cfg.LLMTimeout, log)
	// Background jobs (River over SQLite): the queue builds BEFORE the services
	// — the bill/transaction save hooks enqueue the deferred PPU analysis
	// through it — and starts right after they are wired. A queue that cannot
	// start must never block the API: its failure is logged and boot continues
	// (the enqueue adapter degrades to a no-op), and the one-time unit-value
	// backfill simply re-attempts on the next start.
	jobsMgr, err := buildJobs(ctx, cfg, db, settingsRepo, settingsSvc, promptSvc, billExtractor, insights, log)
	if err != nil {
		log.Error("background jobs disabled for this run", "error", err)
	}
	billAnalyzer := riverPurchaseAnalyzer{manager: jobsMgr}

	billSvc := service.NewBillService(bills, billScans, billExtractor, settingsSvc,
		promptSvc, accounts, categories, stores, products, productMappings, budgets, transactions, fxSvc,
		cfg.BillsPath, billAnalyzer, cfg.LLMTimeout, log)
	offerSearches := repository.NewOfferSearchRepository(db)
	cartSearchSvc := service.NewOfferSearchService(offerSearches, products, stores, settingsSvc,
		promptSvc, billExtractor, cfg.LLMTimeout, log)
	analyticsRepo := repository.NewAnalyticsRepository(db)
	analyticsSvc := service.NewAnalyticsService(analyticsRepo, settingsSvc, fxSvc, storeSvc)

	svc := service.New(accounts, categories, storeSvc, productSvc, stores, products, productMappings,
		transactions, budgets, summary, settingsSvc, billSvc, cartSearchSvc, fxSvc, analyticsSvc,
		promptSvc, service.NewInsightService(insights))

	if jobsMgr != nil {
		if err := jobsMgr.Start(ctx); err != nil {
			jobsMgr.Close()
			jobsMgr = nil
			log.Error("background jobs disabled for this run", "error", err)
		} else if err := enqueueUnitValueBackfill(ctx, db, settingsRepo, jobsMgr, log); err != nil {
			log.Warn("unit-value backfill not enqueued", "error", err)
		}
	}

	// River's web UI, embedded (no extra process): mounted at /riverui on the
	// same server unauthenticated — the same posture as the API on a
	// local-network host. Its background services stop with the boot context,
	// which the signal handler cancels at shutdown.
	var uiHandler http.Handler
	if jobsMgr != nil {
		uiHandler = startRiverUI(ctx, jobsMgr, log) // nil on failure: run without it
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      api.NewRouter(cfg, log, svc, uiHandler),
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
		// Stop the scan workers and the job queue before the DB pool closes
		// (scans never wait for a running extraction — those resume on the
		// next start; the queue drains its jobs, cancelling if it cannot).
		billSvc.Close()
		cartSearchSvc.Close()
		if jobsMgr != nil {
			jobsStopCtx, jobsStopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
			if err := jobsMgr.Stop(jobsStopCtx); err != nil {
				log.Warn("river graceful stop interrupted; cancelling queued work", "error", err)
			}
			jobsStopCancel()
			cancelCtx, cancelCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := jobsMgr.StopAndCancel(cancelCtx); err != nil {
				log.Warn("river cancel stop failed", "error", err)
			}
			cancelCancel()
		}
	}
	return nil
}

// riverPurchaseAnalyzer implements service.PurchaseAnalyzer over the River
// job manager: the deferred PPU analysis of an accepted purchase is one
// enqueued payload. A nil manager (the queue failed to build) degrades the
// enqueue to a silent no-op — the insight is lost for that purchase only.
type riverPurchaseAnalyzer struct{ manager *jobs.Manager }

func (a riverPurchaseAnalyzer) AnalyzeBill(ctx context.Context, billID int64) error {
	return a.insert(ctx, jobs.BillPPUAnalysisArgs{BillID: billID})
}

func (a riverPurchaseAnalyzer) AnalyzeTransaction(ctx context.Context, transactionID int64) error {
	return a.insert(ctx, jobs.TransactionPPUAnalysisArgs{TransactionID: transactionID})
}

func (a riverPurchaseAnalyzer) insert(ctx context.Context, args river.JobArgs) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.Insert(ctx, args, nil)
}

// buildJobs wires the River job queue (its own single-connection pool over
// the same SQLite file) with its workers but does not start it — the caller
// starts it once the services that enqueue into it exist. The returned
// manager is nil when the queue could not be built — boot continues without
// it.
func buildJobs(
	ctx context.Context,
	cfg config.Config,
	db *sql.DB,
	settingsRepo *repository.SettingsRepository,
	settingsSvc *service.SettingsService,
	promptSvc *service.AIPromptService,
	extractor *extractor.Extractor,
	insights *repository.InsightRepository,
	log *slog.Logger,
) (*jobs.Manager, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewUnitValueBackfillWorker(
		repository.NewProductRepository(db),
		repository.NewBillRepository(db),
		repository.NewTransactionRepository(db),
		settingsRepo,
		settingsSvc,
		promptSvc,
		extractor,
		cfg.LLMTimeout,
		log,
	))
	river.AddWorker(workers, jobs.NewBillPPUAnalysisWorker(
		insights,
		insights,
		insights,
		repository.NewProductRepository(db),
		settingsSvc,
		promptSvc,
		extractor,
		cfg.InsightThresholdPct,
		cfg.LLMTimeout,
		log,
	))
	river.AddWorker(workers, jobs.NewTransactionPPUAnalysisWorker(
		insights,
		insights,
		insights,
		repository.NewProductRepository(db),
		settingsSvc,
		promptSvc,
		extractor,
		cfg.InsightThresholdPct,
		cfg.LLMTimeout,
		log,
	))
	return jobs.NewManager(ctx, cfg.DBPath, cfg.LLMTimeout, workers, log)
}

// startRiverUI embeds River's web UI (riverqueue.com/riverui) at /riverui as
// an http.Handler serving on the same server. It runs no extra process and adds
// no auth — on a LAN host the API is equally unauthenticated. A nil handler is
// returned when the UI cannot be built, so boot continues without it; the
// handler's background services stop when ctx (the boot signal context) is
// canceled at shutdown.
func startRiverUI(ctx context.Context, mgr *jobs.Manager, log *slog.Logger) http.Handler {
	endpoints := riverui.NewEndpoints(mgr.Client(), nil)
	ui, err := riverui.NewHandler(&riverui.HandlerOpts{
		Endpoints: endpoints,
		Logger:    log,
		Prefix:    riverUIPrefix,
	})
	if err != nil {
		log.Error("river ui disabled for this run", "error", err)
		return nil
	}
	if err := ui.Start(ctx); err != nil {
		log.Error("river ui failed to start", "error", err)
		return nil
	}
	log.Info("river ui available", "path", riverUIPrefix)
	return ui
}

// enqueueUnitValueBackfill inserts the one-time unit-value job (unique by
// kind, so concurrent boots race harmlessly) unless the completion marker
// exists or nothing is left to fill; a database with nothing to do gets its
// marker directly, without queue churn.
func enqueueUnitValueBackfill(
	ctx context.Context,
	db *sql.DB,
	settingsRepo *repository.SettingsRepository,
	mgr *jobs.Manager,
	log *slog.Logger,
) error {
	if _, err := settingsRepo.Get(ctx, jobs.UnitValueCompletionKey); err == nil {
		return nil // already finished in a previous run
	} else if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("read completion marker: %w", err)
	}

	products := repository.NewProductRepository(db)
	bills := repository.NewBillRepository(db)
	transactions := repository.NewTransactionRepository(db)
	for _, list := range []func(context.Context, int) ([]domain.UnitValueRow, error){
		products.ProductsMissingUnitValue,
		bills.BillItemsMissingUnitValue,
		transactions.TransactionItemsMissingUnitValue,
	} {
		rows, err := list(ctx, 1)
		if err != nil {
			return fmt.Errorf("count backfill candidates: %w", err)
		}
		if len(rows) > 0 {
			// Unique per kind (the kind always rides in the unique key unless
			// ExcludeKind is set): concurrent boots race harmlessly, and the
			// default ByState keeps completed/duplicate protection while
			// discarded jobs can be re-enqueued on the next boot.
			return mgr.Insert(ctx, jobs.UnitValueBackfillArgs{}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{ByQueue: true},
			})
		}
	}

	// Nothing left to resolve (everything filled elsewhere, or a fresh
	// database): mark done so boot stops checking.
	if err := settingsRepo.Put(ctx, jobs.UnitValueCompletionKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("write completion marker (nothing to fill): %w", err)
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

// newLogger builds the process logger. Records go to stdout and — unless
// LOG_FILE is "none" — are appended to LOG_FILE (./data/server.log by
// default) so searches and scans stay traceable across restarts. A log file
// that cannot be opened degrades to stdout-only rather than blocking boot.
func newLogger(cfg config.Config) *slog.Logger {
	writers := []io.Writer{os.Stdout}
	if cfg.LogFile != "" && cfg.LogFile != "none" {
		if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "server: create log directory %s: %v\n", filepath.Dir(cfg.LogFile), err)
		} else if f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "server: open log file %s: %v\n", cfg.LogFile, err)
		} else {
			writers = append(writers, f)
		}
	}

	out := io.MultiWriter(writers...)
	var handler slog.Handler = slog.NewTextHandler(out, &slog.HandlerOptions{Level: cfg.LogLevel})
	if cfg.IsProduction() {
		handler = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: cfg.LogLevel})
	}
	return slog.New(handler)
}
