-- Scheduled Actions: the unified scheduler for planned money movements.
-- One engine hosts every recurrence the plan layer needs: bag funding and
-- bag rebalancing (virtual, auto-applied — they touch no stored account
-- balance), account transfers and transactions (real, confirm-first — the
-- system must never write balances the bank has not confirmed), and
-- purchases (a checklist: no financial effect until the purchase is tagged).
--
-- Payloads are typed per kind in the service layer (kind-switched, no
-- polymorphism in SQL); payload_json holds the validated struct.
--
-- Recurrence is once/monthly/yearly. Monthly materializes one run per month
-- at day_of_month (capped at 28 so a missing date never skips a month);
-- yearly adds month_of_year (Nebenkosten, insurance renewals); once has a
-- single start_date run. Runs are materialized lazily (EnsureScheduledUpcoming
-- from the scheduler's list read and the dashboard summary read, bounded
-- catch-up into the past, up to +2 months ahead): history is created as
-- facts, never virtual — the same decision the monthly budgets made.
--
-- History retention: deleting an action with applied runs refuses (past runs
-- and their materialized effects are history); archived=1 is the soft path
-- (hidden everywhere, keeps history). Deleting a never-applied action is a
-- plain delete and its run rows cascade with it (the reminders of a dropped
-- plan — the archive exists as the path that keeps them).
--
-- The auto-apply invariant: the system auto-executes only movements that
-- touch no stored account balance. Confirm-first kinds stay reminders until
-- one user click applies them.
CREATE TABLE scheduled_actions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL,
    kind          TEXT    NOT NULL
                  CHECK (kind IN ('bag_funding','bag_rebalance','account_transfer',
                                  'transaction','purchase')),
    payload_json  TEXT    NOT NULL,
    recurrence    TEXT    NOT NULL CHECK (recurrence IN ('once','monthly','yearly')),
    start_date    TEXT    NOT NULL CHECK (start_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    day_of_month  INTEGER CHECK (day_of_month IS NULL OR day_of_month BETWEEN 1 AND 28),
    month_of_year INTEGER CHECK (month_of_year IS NULL OR month_of_year BETWEEN 1 AND 12),
    archived      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- One run per (action, due date): the lazy catch-up is idempotent through
-- INSERT OR IGNORE. Deleting an action cascades its runs — never SET NULL,
-- which would orphan rows whose NULL action_id the unique index can never
-- de-duplicate again.
CREATE TABLE scheduled_action_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    action_id   INTEGER NOT NULL REFERENCES scheduled_actions (id) ON DELETE CASCADE,
    due_date    TEXT    NOT NULL CHECK (due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    status      TEXT    NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','applied','dismissed','skipped')),
    -- allocation = the auto-applied virtual movement wrote into the bag
    -- ledger; transaction = the confirm-first apply wrote a transaction row.
    result_type TEXT CHECK (result_type IS NULL OR result_type IN ('allocation','transaction')),
    result_id   INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (action_id, due_date)
);

CREATE INDEX idx_scheduled_runs_due ON scheduled_action_runs (due_date, status);