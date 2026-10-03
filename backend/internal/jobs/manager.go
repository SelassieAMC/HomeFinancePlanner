package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

// Manager owns the River job queue: its dedicated SQLite pool (a separate
// single-connection pool over the same database file — River's sqlite driver
// wants one writer), the queue's own migration state, enqueueing and the
// graceful start/stop lifecycle. The application database keeps its own pool;
// WAL allows both to read the same file.
type Manager struct {
	pool     *sql.DB
	client   *river.Client[*sql.Tx]
	migrator *rivermigrate.Migrator[*sql.Tx]
	log      *slog.Logger
}

// NewManager opens the queue pool, applies the River schema migrations and
// builds (but does not start) the client. workers must already carry the
// registered job workers. aiTimeout is the per-AI-call budget the retry
// policy derives its backoff floor from — the floor is deliberately the call
// timeout itself so a retry never lands while a slow local model is still
// generating the abandoned previous call (see unit_value.go's normalization
// note: an instant retry queues behind it and times out too).
func NewManager(ctx context.Context, dbPath string, aiTimeout time.Duration, workers *river.Workers, log *slog.Logger) (*Manager, error) {
	if aiTimeout <= 0 {
		aiTimeout = 5 * time.Minute
	}
	pool, err := sql.Open("sqlite", riverDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open river sqlite pool: %w", err)
	}
	// Single writer + single keeper connection: River's SQLite driver
	// serializes writes, and the pragmas (journal mode, foreign keys, busy
	// timeout, immediate transactions) must ride in the DSN because
	// database/sql may open fresh connections behind the pool's back.
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxIdleTime(0)
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping river sqlite pool: %w", err)
	}

	driver := riversqlite.New(pool)
	migrator, err := rivermigrate.New[*sql.Tx](driver, &rivermigrate.Config{})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, &rivermigrate.MigrateOpts{}); err != nil {
		pool.Close()
		return nil, fmt.Errorf("river migrations: %w", err)
	}

	client, err := river.NewClient(driver, &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 2},
		},
		Workers:     workers,
		RetryPolicy: newRetryPolicy(aiTimeout),
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("build river client: %w", err)
	}
	return &Manager{pool: pool, client: client, migrator: migrator, log: log}, nil
}

// riverDSN builds the sqlite DSN the River pool needs: the same pragmas the
// application pool uses, plus the immediate-transaction lock River's sqlite
// driver expects for its job-poll loops.
func riverDSN(dbPath string) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate", dbPath)
}

// Client exposes the river client for integrations that run their own
// queries/commands against the queue (the River web UI's endpoints bundle).
// The Manager keeps ownership of start/stop; the accessor never hands the
// queue's lifecycle away.
func (m *Manager) Client() *river.Client[*sql.Tx] {
	return m.client
}

// Start begins the queue's workers; it returns once the client is running.
func (m *Manager) Start(ctx context.Context) error {
	return m.client.Start(ctx)
}

// Stop drains the queue: wait workers idle and return, matching the server's
// graceful shutdown path.
func (m *Manager) Stop(ctx context.Context) error {
	return m.client.Stop(ctx)
}

// StopAndCancel is the hard stop: cancels running jobs (their contexts) and
// returns. Wired after Stop for shutdowns that outlasted the drain budget.
func (m *Manager) StopAndCancel(ctx context.Context) error {
	return m.client.StopAndCancel(ctx)
}

// Close closes the queue's pool (only meaningful for a client that never
// started — a started one must Stop first).
func (m *Manager) Close() error {
	return m.pool.Close()
}

// Insert enqueues one job; a unique collision (a duplicate under the caller's
// unique options) is treated as success — the job is already queued.
func (m *Manager) Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) error {
	res, err := m.client.Insert(ctx, args, opts)
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", args.Kind(), err)
	}
	if res.UniqueSkippedAsDuplicate {
		m.log.Debug("job already queued or running", slog.String("kind", args.Kind()))
	}
	return nil
}

// llmRetryPolicy paces failed jobs around slow local vision-style models: the
// FIRST retry waits the full AI-call timeout (×2, so the abandoned generation
// has certainly drained from the model server), then grows exponentially with
// the attempt number, capped at 24h. River's stock policy (≈15s first retry)
// would send the next attempt into the tail of the still-running request.
type llmRetryPolicy struct {
	floor time.Duration
}

func newRetryPolicy(aiTimeout time.Duration) *llmRetryPolicy {
	floor := 2 * aiTimeout
	if floor < 30*time.Second {
		floor = 30 * time.Second
	}
	return &llmRetryPolicy{floor: floor}
}

// NextRetry implements river.RetryPolicy.
func (p *llmRetryPolicy) NextRetry(job *rivertype.JobRow) time.Time {
	backoff := p.floor
	for i := 1; i < job.Attempt && i < 8; i++ {
		backoff *= 2 // floor × 2^(attempt-1), saturating below via the cap
	}
	if backoff > 24*time.Hour || math.IsInf(float64(backoff), 0) {
		backoff = 24 * time.Hour
	}
	return time.Now().Add(backoff)
}
