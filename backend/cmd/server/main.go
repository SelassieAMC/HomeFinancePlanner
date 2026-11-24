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
	"github.com/riverqueue/river/rivertype"

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
	workerMode := flag.Bool("worker", false, "run only the background job queue (no HTTP server)")
	ping := flag.Bool("ping", false, "verify the database is reachable (used by the worker healthcheck)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := newLogger(cfg)

	if *healthcheck {
		return doHealthcheck(cfg, log)
	}
	if *ping {
		return doDBPing(cfg)
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
	userSessions := repository.NewSessionRepository(db)
	userSvc := service.NewUserService(repository.NewUserRepository(db), userSessions, cfg.AvatarsPath)

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

	// Background jobs (River over SQLite). The pipeline enqueuer is created
	// before the services — they hold it while their workers are registered into
	// the queue — and its manager is attached once the queue is built. A queue
	// that cannot build must never block the API: its failure is logged, boot
	// continues, the enqueuer degrades to a no-op, and the services refuse the
	// scan/search requests outright (their SetJobEnqueuer is left unset).
	jobEnqueuer := &pipelineJobs{}

	productSvc := service.NewProductService(products, categories, cfg.ProductsPath,
		productMappings, billExtractor, settingsSvc, promptSvc, cfg.LLMTimeout, log)
	billSvc := service.NewBillService(bills, billScans, billExtractor, settingsSvc,
		promptSvc, accounts, categories, stores, products, productMappings, budgets,
		repository.NewBagRepository(db), transactions, fxSvc,
		cfg.BillsPath, jobEnqueuer, cfg.LLMTimeout, log)
	offerSearches := repository.NewOfferSearchRepository(db)
	cartSearchSvc := service.NewOfferSearchService(offerSearches, products, stores, settingsSvc,
		promptSvc, billExtractor, cfg.LLMTimeout, log)

	// Both processes register the same workers: River validates a job's kind
	// against the client's Workers bundle at Insert time, so even a
	// producer-only client must know every kind it enqueues. The difference is
	// execution — only the worker calls Start. The API registers the workers
	// (so Insert passes) but never starts its client, so a slow AI scan or a
	// crashed worker cannot take the API down, and an API crash leaves the
	// worker processing unaffected. The worker owns the boot-time enqueueing
	// (backfill, stuck-scan recovery) that the API used to do.
	workers := buildWorkers(cfg, db, settingsRepo, settingsSvc, promptSvc, billExtractor, insights, fxSvc, productSvc, billSvc, cartSearchSvc, log)

	if *workerMode {
		jobsMgr, err := jobs.NewManager(ctx, cfg.DBPath, cfg.LLMTimeout, workers, log)
		if err != nil {
			return fmt.Errorf("build job queue: %w", err)
		}
		jobEnqueuer.manager = jobsMgr
		productSvc.SetJobEnqueuer(jobEnqueuer)
		billSvc.SetJobEnqueuer(jobEnqueuer)
		cartSearchSvc.SetJobEnqueuer(jobEnqueuer)
		return runWorker(ctx, cfg, db, settingsRepo, billSvc, cartSearchSvc, jobsMgr, log)
	}

	// API path: the same workers registered but never started — a producer
	// client for enqueueing and the River web UI. Its failure must never
	// block the API: the enqueuer degrades to a no-op and the services refuse
	// the scan/search requests outright.
	jobsMgr, err := jobs.NewManager(ctx, cfg.DBPath, cfg.LLMTimeout, workers, log)
	if err != nil {
		log.Error("background jobs disabled for this run", "error", err)
	}
	jobEnqueuer.manager = jobsMgr
	productSvc.SetJobEnqueuer(jobEnqueuer)
	billSvc.SetJobEnqueuer(jobEnqueuer)
	cartSearchSvc.SetJobEnqueuer(jobEnqueuer)

	analyticsRepo := repository.NewAnalyticsRepository(db)
	analyticsSvc := service.NewAnalyticsService(analyticsRepo, settingsSvc, fxSvc, storeSvc)

	svc := service.New(accounts, repository.NewAccountGroupRepository(db), categories, storeSvc, productSvc, stores, products, productMappings,
		transactions, budgets, summary, settingsSvc, billSvc, cartSearchSvc, fxSvc, analyticsSvc,
		promptSvc, service.NewInsightService(insights),
		repository.NewGoalRepository(db), repository.NewBagRepository(db),
		repository.NewScheduledActionRepository(db), userSvc)

	// The one-time admin seed: the first boot on an empty users table creates
	// admin/admin (a boot warning names it — change the password afterwards).
	userSvc.EnsureSeedAdmin(ctx)

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

	// Graceful shutdown on SIGINT/SIGTERM. The API never started the job
	// queue, so there is nothing to drain — the worker process owns its own
	// shutdown.
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
	}
	return nil
}

// pipelineJobs adapts the River job manager to the service enqueue contracts:
// the deferred purchase analysis (service.PurchaseAnalyzer) and the three
// pipeline jobs (service.JobEnqueuer). It is created before the services — so
// they can hold it while their workers are registered into the queue — and its
// manager is attached once the queue is built. A nil manager (the queue failed
// to build) degrades the enqueue to a silent no-op; the services nil-guard the
// same way and refuse the scan/search request outright.
type pipelineJobs struct{ manager *jobs.Manager }

func (p *pipelineJobs) AnalyzeBill(ctx context.Context, billID int64) error {
	return p.insert(ctx, jobs.BillPPUAnalysisArgs{BillID: billID}, nil)
}

func (p *pipelineJobs) AnalyzeTransaction(ctx context.Context, transactionID int64) error {
	return p.insert(ctx, jobs.TransactionPPUAnalysisArgs{TransactionID: transactionID}, nil)
}

// AnalyzeTransactionCategories enqueues the category anomaly check of a
// saved manual expense transaction.
func (p *pipelineJobs) AnalyzeTransactionCategories(ctx context.Context, transactionID int64) error {
	return p.insert(ctx, jobs.TransactionCategoryAnalysisArgs{TransactionID: transactionID}, nil)
}

func (p *pipelineJobs) EnqueueBillScan(ctx context.Context, token string) error {
	return p.insert(ctx, service.BillScanArgs{Token: token}, &river.InsertOpts{
		Queue: jobs.QueueBillScan,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true, // a re-enqueue of the same token is a no-op
		},
	})
}

func (p *pipelineJobs) EnqueueOfferSearch(ctx context.Context, token string) error {
	return p.insert(ctx, service.OfferSearchArgs{Token: token}, &river.InsertOpts{
		Queue: jobs.QueueOfferSearch,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	})
}

func (p *pipelineJobs) EnqueueProductNormalization(ctx context.Context) error {
	return p.insert(ctx, service.ProductNormalizationArgs{}, &river.InsertOpts{
		Queue: jobs.QueueProductNormalization,
		UniqueOpts: river.UniqueOpts{
			// Single-flight: the singleton job, keyed by kind (no args) — refuse
			// a second enqueue while one is queued or running.
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
				rivertype.JobStatePending,
			},
		},
	})
}

func (p *pipelineJobs) insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) error {
	if p.manager == nil {
		return nil
	}
	return p.manager.Insert(ctx, args, opts)
}

// buildWorkers registers every River worker the queue executes. The API
// process never calls it — its producer-only client has no workers — so the
// worker process is the only place the AI-backed jobs run.
func buildWorkers(
	cfg config.Config,
	db *sql.DB,
	settingsRepo *repository.SettingsRepository,
	settingsSvc *service.SettingsService,
	promptSvc *service.AIPromptService,
	extractor *extractor.Extractor,
	insights *repository.InsightRepository,
	fxSvc *service.FXService,
	productSvc *service.ProductService,
	billSvc *service.BillService,
	cartSearchSvc *service.OfferSearchService,
	log *slog.Logger,
) *river.Workers {
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
	river.AddWorker(workers, jobs.NewTransactionCategoryAnalysisWorker(
		insights,
		repository.NewTransactionRepository(db),
		repository.NewCategoryRepository(db),
		insights, // the day-grained category spend read lives on the insight repository
		settingsSvc,
		fxSvc,
		settingsSvc,
		promptSvc,
		extractor,
		cfg.AnomalyRisePct,
		cfg.AnomalyMinBaseline,
		cfg.LLMTimeout,
		log,
	))
	// The three pipeline workers formerly owned by the services (their
	// goroutine pools are gone): receipt scan, offer search and product
	// normalization, each routed to its own queue in NewManager.
	river.AddWorker(workers, service.NewBillScanWorker(billSvc))
	river.AddWorker(workers, service.NewOfferSearchWorker(cartSearchSvc))
	river.AddWorker(workers, service.NewProductNormalizationWorker(productSvc))
	return workers
}

// runWorker is the worker process's whole life: start the queue, enqueue the
// one-time backfill, resume any scans/searches left mid-flight by the last
// stop, then block until a shutdown signal and drain the queue. No HTTP.
func runWorker(
	ctx context.Context,
	cfg config.Config,
	db *sql.DB,
	settingsRepo *repository.SettingsRepository,
	billSvc *service.BillService,
	cartSearchSvc *service.OfferSearchService,
	jobsMgr *jobs.Manager,
	log *slog.Logger,
) error {
	if err := jobsMgr.Start(ctx); err != nil {
		jobsMgr.Close()
		return fmt.Errorf("start job queue: %w", err)
	}
	if err := enqueueUnitValueBackfill(ctx, db, settingsRepo, jobsMgr, log); err != nil {
		log.Warn("unit-value backfill not enqueued", "error", err)
	}
	// Resume scans/searches that were mid-flight when this process last
	// stopped (a clean shutdown cancels their River job but leaves the row
	// analyzing/searching).
	billSvc.RecoverScans(ctx)
	cartSearchSvc.RecoverSearches(ctx)

	log.Info("worker running", "env", cfg.Env, "db", cfg.DBPath)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	sig := <-stop
	log.Info("shutting down", "signal", sig.String())

	// Stop the job queue before the DB pool closes: the queue drains its jobs
	// (a running extraction may take longer than the drain budget, in which
	// case StopAndCancel aborts it and the scan resumes on the next start via
	// RecoverScans), cancelling if it cannot.
	jobsStopCtx, jobsStopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer jobsStopCancel()
	if err := jobsMgr.Stop(jobsStopCtx); err != nil {
		log.Warn("river graceful stop interrupted; cancelling queued work", "error", err)
	}
	cancelCtx, cancelCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCancel()
	if err := jobsMgr.StopAndCancel(cancelCtx); err != nil {
		log.Warn("river cancel stop failed", "error", err)
	}
	return nil
}

// doDBPing opens the database and pings it, exiting 0 when reachable. It is
// the worker container's healthcheck: a worker has no HTTP endpoint to probe,
// so liveness is proxied by "can this process still open and reach SQLite".
func doDBPing(cfg config.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := repository.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
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
