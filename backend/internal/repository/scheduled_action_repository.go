package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

const scheduledActionColumns = `id, name, kind, payload_json, recurrence, start_date, day_of_month, month_of_year, auto_apply, archived, created_at, updated_at`

const scheduledRunColumns = `id, action_id, due_date, status, result_type, result_id, created_at, updated_at`

// ScheduledActionRepository is the SQLite-backed store of the unified
// scheduler: the plan rows and their materialized runs. Runs are INSERT OR
// IGNOREd against UNIQUE (action_id, due_date) — the lazy catch-up stays
// idempotent under concurrent reads.
type ScheduledActionRepository struct{ db *sql.DB }

func NewScheduledActionRepository(db *sql.DB) *ScheduledActionRepository {
	return &ScheduledActionRepository{db: db}
}

// ListActions returns scheduled actions, archived ones excluded unless asked.
func (r *ScheduledActionRepository) ListActions(ctx context.Context, includeArchived bool) ([]domain.ScheduledAction, error) {
	q := `SELECT ` + scheduledActionColumns + ` FROM scheduled_actions `
	if !includeArchived {
		q += `WHERE archived = 0 `
	}
	q += `ORDER BY id ASC`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list scheduled actions: %w", err)
	}
	defer rows.Close()

	out := []domain.ScheduledAction{}
	for rows.Next() {
		a, err := scanScheduledAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ScheduledActionRepository) GetActionByID(ctx context.Context, id int64) (domain.ScheduledAction, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+scheduledActionColumns+` FROM scheduled_actions WHERE id = ?`, id)
	a, err := scanScheduledAction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ScheduledAction{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ScheduledAction{}, fmt.Errorf("get scheduled action %d: %w", id, err)
	}
	return a, nil
}

// ActiveActionsTargetingBag counts non-archived actions whose payload moves
// money into or out of the bag (funding target, rebalance source or
// destination) — the guard that refuses deleting a bag an active schedule
// still funds or drains.
func (r *ScheduledActionRepository) ActiveActionsTargetingBag(ctx context.Context, bagID int64) ([]domain.ScheduledAction, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+scheduledActionColumns+` FROM scheduled_actions
		WHERE archived = 0
		  AND kind IN ('bag_funding','bag_rebalance')
		  AND (? IN (json_extract(payload_json, '$.bag_id'),
		             json_extract(payload_json, '$.from_bag_id'),
		             json_extract(payload_json, '$.to_bag_id')))
		ORDER BY id ASC`, bagID)
	if err != nil {
		return nil, fmt.Errorf("find schedules targeting bag %d: %w", bagID, err)
	}
	defer rows.Close()

	out := []domain.ScheduledAction{}
	for rows.Next() {
		a, err := scanScheduledAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ScheduledActionRepository) CreateAction(ctx context.Context, a domain.ScheduledAction) (domain.ScheduledAction, error) {
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return domain.ScheduledAction{}, fmt.Errorf("encode schedule payload: %w", err)
	}
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO scheduled_actions
			(name, kind, payload_json, recurrence, start_date, day_of_month, month_of_year, auto_apply, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, string(a.Kind), string(payload), string(a.Recurrence), a.StartDate,
		nullableDay(a.DayOfMonth), nullableDay(a.MonthOfYear), boolInt(a.AutoApply), boolInt(a.Archived), now, now)
	if err != nil {
		return domain.ScheduledAction{}, mapWriteError("create scheduled action", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.ScheduledAction{}, fmt.Errorf("scheduled action insert id: %w", err)
	}
	return r.GetActionByID(ctx, id)
}

// UpdateAction replaces the editable fields of one action. Applied history on
// scheduled_action_runs is untouched here — the future-only edit rule lives in
// the service, which decides which pending runs to regenerate.
func (r *ScheduledActionRepository) UpdateAction(ctx context.Context, a domain.ScheduledAction) (domain.ScheduledAction, error) {
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return domain.ScheduledAction{}, fmt.Errorf("encode schedule payload: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_actions
		SET name = ?, kind = ?, payload_json = ?, recurrence = ?, start_date = ?,
		    day_of_month = ?, month_of_year = ?, auto_apply = ?, archived = ?, updated_at = ?
		WHERE id = ?`,
		a.Name, string(a.Kind), string(payload), string(a.Recurrence), a.StartDate,
		nullableDay(a.DayOfMonth), nullableDay(a.MonthOfYear), boolInt(a.AutoApply), boolInt(a.Archived),
		time.Now().Unix(), a.ID)
	if err != nil {
		return domain.ScheduledAction{}, mapWriteError("update scheduled action", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ScheduledAction{}, domain.ErrNotFound
	}
	return r.GetActionByID(ctx, a.ID)
}

// DeleteAction removes a never-applied action outright — its pending,
// dismissed and skipped run rows cascade. Applied runs would cascade too,
// which is exactly why the service refuses this delete when history exists.
func (r *ScheduledActionRepository) DeleteAction(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_actions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete scheduled action %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// MaterializeRuns inserts the given due dates for one action, ignoring dates
// that already have a run (UNIQUE action_id+due_date) — the lazy catch-up is
// idempotent. Returns how many rows were newly created.
func (r *ScheduledActionRepository) MaterializeRuns(ctx context.Context, actionID int64, dueDates []string) (int64, error) {
	if len(dueDates) == 0 {
		return 0, nil
	}
	now := time.Now().Unix()
	var created int64
	for _, d := range dueDates {
		res, err := r.db.ExecContext(ctx, `
			INSERT INTO scheduled_action_runs (action_id, due_date, status, created_at, updated_at)
			VALUES (?, ?, 'pending', ?, ?)
			ON CONFLICT (action_id, due_date) DO NOTHING`,
			actionID, d, now, now)
		if err != nil {
			return created, fmt.Errorf("materialize run %s for action %d: %w", d, actionID, err)
		}
		n, _ := res.RowsAffected()
		created += n
	}
	return created, nil
}

// DeletePendingRuns removes every still-pending run of one action — the edit
// rule regenerates the not-yet-materialized schedule; applied, dismissed and
// skipped runs (history and acknowledgements) stay untouched.
func (r *ScheduledActionRepository) DeletePendingRuns(ctx context.Context, actionID int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM scheduled_action_runs WHERE action_id = ? AND status = 'pending'`, actionID)
	if err != nil {
		return fmt.Errorf("delete pending runs of action %d: %w", actionID, err)
	}
	return nil
}

// ListRuns returns runs matching the filters, due date ascending (overdue
// pending runs naturally sort first), each carrying its action's row data —
// the read the to-do list and the dashboard "Coming up" card feed on.
func (r *ScheduledActionRepository) ListRuns(ctx context.Context, f domain.ScheduleRunFilters) ([]domain.ScheduleRunView, error) {
	q := `
		SELECT
			r.id, r.action_id, r.due_date, r.status, r.result_type, r.result_id, r.created_at, r.updated_at,
			a.name, a.kind, a.payload_json
		FROM scheduled_action_runs r
		JOIN scheduled_actions a ON a.id = r.action_id
		WHERE a.archived = 0`
	args := []any{}
	if f.ActionID > 0 {
		q += ` AND r.action_id = ?`
		args = append(args, f.ActionID)
	}
	if len(f.Statuses) > 0 {
		placeholders := make([]string, 0, len(f.Statuses))
		for _, s := range f.Statuses {
			placeholders = append(placeholders, "?")
			args = append(args, string(s))
		}
		q += ` AND r.status IN (` + strings.Join(placeholders, ",") + `)`
	}
	if f.DueFrom != "" {
		q += ` AND r.due_date >= ?`
		args = append(args, f.DueFrom)
	}
	if f.DueTo != "" {
		q += ` AND r.due_date <= ?`
		args = append(args, f.DueTo)
	}
	q += ` ORDER BY r.due_date ASC, r.id ASC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list scheduled runs: %w", err)
	}
	defer rows.Close()

	out := []domain.ScheduleRunView{}
	for rows.Next() {
		v, err := scanScheduledRunView(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled run: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *ScheduledActionRepository) GetRunByID(ctx context.Context, id int64) (domain.ScheduleRunView, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT
			r.id, r.action_id, r.due_date, r.status, r.result_type, r.result_id, r.created_at, r.updated_at,
			a.name, a.kind, a.payload_json
		FROM scheduled_action_runs r
		JOIN scheduled_actions a ON a.id = r.action_id
		WHERE r.id = ?`, id)
	v, err := scanScheduledRunView(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ScheduleRunView{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ScheduleRunView{}, fmt.Errorf("get scheduled run %d: %w", id, err)
	}
	return v, nil
}

// UpdateRunStatus moves a run between states (e.g. pending → applied). The
// caller validates the transition; the update only reports whether a row was
// found.
func (r *ScheduledActionRepository) UpdateRunStatus(ctx context.Context, runID int64, status domain.ScheduleRunStatus, resultType *string, resultID *int64) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_action_runs
		SET status = ?, result_type = ?, result_id = ?, updated_at = ?
		WHERE id = ?`,
		string(status), resultType, resultID, time.Now().Unix(), runID)
	if err != nil {
		return fmt.Errorf("update scheduled run %d: %w", runID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListRunsByStatus lists the runs of one action in one status, oldest due
// first — the auto-apply loop's input (it applies due runs in due order).
func (r *ScheduledActionRepository) ListRunsByStatus(ctx context.Context, actionID int64, status domain.ScheduleRunStatus) ([]domain.ScheduledActionRun, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+scheduledRunColumns+` FROM scheduled_action_runs
		WHERE action_id = ? AND status = ? ORDER BY due_date ASC, id ASC`,
		actionID, string(status))
	if err != nil {
		return nil, fmt.Errorf("list runs of action %d: %w", actionID, err)
	}
	defer rows.Close()

	out := []domain.ScheduledActionRun{}
	for rows.Next() {
		run, err := scanScheduledRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func scanScheduledAction(row interface{ Scan(dest ...any) error }) (domain.ScheduledAction, error) {
	var (
		a          domain.ScheduledAction
		kind       string
		payload    string
		recurrence string
		day, month sql.NullInt64
		autoApply  int64
		archived   int64
		created, u int64
	)
	if err := row.Scan(&a.ID, &a.Name, &kind, &payload, &recurrence, &a.StartDate,
		&day, &month, &autoApply, &archived, &created, &u); err != nil {
		return domain.ScheduledAction{}, err
	}
	a.Kind = domain.ScheduleKind(kind)
	if err := json.Unmarshal([]byte(payload), &a.Payload); err != nil {
		return domain.ScheduledAction{}, fmt.Errorf("decode schedule payload of action %d: %w", a.ID, err)
	}
	a.Recurrence = domain.ScheduleRecurrence(recurrence)
	if day.Valid {
		v := int(day.Int64)
		a.DayOfMonth = &v
	}
	if month.Valid {
		v := int(month.Int64)
		a.MonthOfYear = &v
	}
	a.AutoApply = autoApply != 0
	a.Archived = archived != 0
	a.CreatedAt = time.Unix(created, 0).UTC()
	a.UpdatedAt = time.Unix(u, 0).UTC()
	return a, nil
}

func scanScheduledRun(row interface{ Scan(dest ...any) error }) (domain.ScheduledActionRun, error) {
	var (
		run          domain.ScheduledActionRun
		status       string
		resultType   sql.NullString
		resultID     sql.NullInt64
		created, upd int64
	)
	if err := row.Scan(&run.ID, &run.ActionID, &run.DueDate, &status, &resultType,
		&resultID, &created, &upd); err != nil {
		return domain.ScheduledActionRun{}, err
	}
	run.Status = domain.ScheduleRunStatus(status)
	if resultType.Valid {
		v := resultType.String
		run.ResultType = &v
	}
	if resultID.Valid {
		v := resultID.Int64
		run.ResultID = &v
	}
	run.CreatedAt = time.Unix(created, 0).UTC()
	run.UpdatedAt = time.Unix(upd, 0).UTC()
	return run, nil
}

func scanScheduledRunView(row interface{ Scan(dest ...any) error }) (domain.ScheduleRunView, error) {
	var (
		v            domain.ScheduleRunView
		status       string
		resultType   sql.NullString
		resultID     sql.NullInt64
		created, upd int64
		kind         string
		payload      string
	)
	err := row.Scan(&v.ID, &v.ActionID, &v.DueDate, &status, &resultType, &resultID,
		&created, &upd, &v.ActionName, &kind, &payload)
	if err != nil {
		return domain.ScheduleRunView{}, err
	}
	v.Status = domain.ScheduleRunStatus(status)
	if resultType.Valid {
		s := resultType.String
		v.ResultType = &s
	}
	if resultID.Valid {
		s := resultID.Int64
		v.ResultID = &s
	}
	v.CreatedAt = time.Unix(created, 0).UTC()
	v.UpdatedAt = time.Unix(upd, 0).UTC()
	v.Kind = domain.ScheduleKind(kind)
	if err := json.Unmarshal([]byte(payload), &v.Schedule); err != nil {
		return domain.ScheduleRunView{}, fmt.Errorf("decode schedule payload of action %d: %w", v.ActionID, err)
	}
	return v, nil
}

// nullableDay converts a nullable small-int action field into a driver value.
func nullableDay(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
