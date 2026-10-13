package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// ScheduleHandler handles /api/v1/scheduled-actions: the unified scheduler's
// plan rows, their materialized runs, and the confirm-first apply/dismiss
// flow. Today's date is decided server-side (like the budget rollover and the
// transaction future-date rule) — the client never supplies a date the lazily
// materialized run history keys on.
type ScheduleHandler struct {
	Svc *service.ScheduledActionService
}

type scheduleRequest struct {
	Name        string                 `json:"name"`
	Kind        string                 `json:"kind"`
	Payload     domain.SchedulePayload `json:"payload"`
	Recurrence  string                 `json:"recurrence"`
	StartDate   string                 `json:"start_date"`
	DayOfMonth  *int                   `json:"day_of_month"`
	MonthOfYear *int                   `json:"month_of_year"`
}

func (h *ScheduleHandler) input(r scheduleRequest) service.ScheduledActionInput {
	return service.ScheduledActionInput{
		Name:        r.Name,
		Kind:        domain.ScheduleKind(r.Kind),
		Payload:     r.Payload,
		Recurrence:  domain.ScheduleRecurrence(r.Recurrence),
		StartDate:   r.StartDate,
		DayOfMonth:  r.DayOfMonth,
		MonthOfYear: r.MonthOfYear,
	}
}

// serverToday is the scheduler's reference date (server-local, like the
// budget rollover's currentMonth).
func serverToday() string { return time.Now().Format("2006-01-02") }

// List returns the active schedules after the lazy catch-up.
func (h *ScheduleHandler) List(w http.ResponseWriter, r *http.Request) {
	actions, err := h.Svc.List(r.Context(), serverToday())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, actions)
}

// Create stores a new schedule.
func (h *ScheduleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := h.Svc.Create(r.Context(), h.input(req))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, a)
}

// Update reshapes a schedule (future occurrences only — pending runs are
// regenerated, applied history stays).
func (h *ScheduleHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req scheduleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := h.Svc.Update(r.Context(), id, h.input(req))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, a)
}

// Archive is the soft stop: hidden everywhere, history kept, pending
// reminders dropped.
func (h *ScheduleHandler) Archive(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := h.Svc.Archive(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, a)
}

// Delete removes a never-applied schedule outright (runs cascade); one
// applied run turns the delete into a conflict — archive instead.
func (h *ScheduleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Svc.Delete(r.Context(), id); err != nil {
		respondServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListRuns is the runs read: the to-do list (`?status=pending`, overdue
// naturally first through the due-date ordering) and the dashboard "Coming
// up" window (`?due_from=&due_to=`); statuses accept a comma-separated list.
func (h *ScheduleHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	f := domain.ScheduleRunFilters{}
	if raw := r.URL.Query().Get("action_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			respondError(w, r, http.StatusBadRequest, "action_id must be a positive id")
			return
		}
		f.ActionID = id
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		for _, part := range splitComma(raw) {
			f.Statuses = append(f.Statuses, domain.ScheduleRunStatus(part))
		}
	}
	f.DueFrom = r.URL.Query().Get("due_from")
	f.DueTo = r.URL.Query().Get("due_to")
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			respondError(w, r, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		f.Limit = n
	}
	runs, err := h.Svc.Runs(r.Context(), serverToday(), f)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, runs)
}

// runID parses the {run_id} path value of the /runs/... routes.
func runID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("run_id"), 10, 64)
}

// ApplyRun executes a confirm-first occurrence (transfer / transaction):
// writes the real effect and marks the run applied. Virtual kinds
// auto-applied at materialization already.
func (h *ScheduleHandler) ApplyRun(w http.ResponseWriter, r *http.Request) {
	id, err := runID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid run id")
		return
	}
	view, err := h.Svc.ApplyRun(r.Context(), id, serverToday())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, view)
}

// DismissRun acknowledges a pending occurrence (seen, nothing to do).
func (h *ScheduleHandler) DismissRun(w http.ResponseWriter, r *http.Request) {
	id, err := runID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid run id")
		return
	}
	view, err := h.Svc.DismissRun(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, view)
}

// SkipRun marks a pending occurrence not applicable this time; the row stays.
func (h *ScheduleHandler) SkipRun(w http.ResponseWriter, r *http.Request) {
	id, err := runID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid run id")
		return
	}
	view, err := h.Svc.SkipRun(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, view)
}

// splitComma splits a comma-separated query parameter into trimmed, non-empty
// parts.
func splitComma(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
