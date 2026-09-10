# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project Overview

**home-finance-planner** — a self-hosted personal/home finance planner.
Users track accounts, transactions, categories, and monthly budgets, and view
a planning dashboard.

- **Frontend:** React 18 + TypeScript + Vite (SPA, served by nginx in production)
- **Backend:** Go 1.26 (stdlib `net/http`, no web framework)
- **Database:** SQLite (single-file, WAL mode, embedded migrations)
- **Deployment:** Docker + docker-compose (frontend nginx proxies `/api` to backend)

## Repository Layout

```
backend/
  cmd/server/            # entrypoint: wiring, graceful shutdown
  internal/
    api/                 # HTTP layer (transport only)
      handlers/          # request decoding / response encoding, DTOs
      middleware/        # logging, recovery, CORS, request ID
      router.go          # route table
    config/              # env-driven configuration
    crypto/              # AES-256-GCM helpers (AI provider API keys at rest)
    domain/              # core entities + domain errors (no dependencies)
    extractor/           # AI receipt extraction (providers, prompt, JSON parsing)
    repository/          # SQLite data access (interfaces + impls), migrations
    service/             # business rules & validation (depends on repository interfaces)
  migrations/            # SQL migration files (embedded, sequential)

frontend/
  src/
    api/                 # typed HTTP client + per-domain API modules
    app/                 # routing, app shell wiring
    components/
      layout/            # AppLayout, Sidebar, Header
      ui/                # reusable presentational primitives (Button, Card, …)
    features/            # feature folders: dashboard, transactions, budgets, accounts, bills, settings
    hooks/               # reusable React hooks
    types/               # shared domain types mirrored from backend DTOs
```

## Architecture Rules (enforced)

- **Layering is one-directional:** `api/handlers → service → repository → db`.
  Handlers never touch `*sql.DB`; services never know about HTTP; repositories
  never know about business rules.
- **`domain` is dependency-free.** No imports of `net/http`, `sql`, or other
  internal packages from `domain`.
- **Dependencies are injected via interfaces.** Services depend on repository
  interfaces defined in `service` (consumer-side), not on concrete types. This
  keeps services unit-testable without SQLite.
- **Money is integer cents (`int64`)** everywhere. Never floats for money.
- **Dates are `time.Time` in Go / ISO-8601 strings on the wire**; budget months
  are `YYYY-MM` strings.
- New tables/columns require a new numbered migration file in
  `backend/migrations/` — never edit an applied migration.

## Commands

Run from the repository root:

| Task | Command |
|---|---|
| Dev backend (go run) | `make dev-backend` |
| Dev frontend (vite) | `make dev-frontend` |
| Full stack in Docker | `make up` / `make down` |
| Backend tests | `make test-backend` |
| Backend lint/vet | `make lint-backend` |
| Frontend typecheck | `make typecheck-frontend` |
| Frontend lint | `make lint-frontend` |
| Rebuild containers | `make build` |

Local dev runs the backend on `:5001` (set `PORT=5001` via `.env`) and Vite on
`:5002`, proxying `/api` to `:5001` (see `frontend/vite.config.ts`). In Docker
the host ports match (API :5001, UI :5002) while container-internal ports stay
fixed: backend `:8080`, nginx `:80`.

## Conventions

### Go
- Package layout is "cmd + internal" — nothing outside `internal` may be
  imported by other repos.
- Errors: repositories return domain errors (`domain.ErrNotFound`,
  `domain.ErrValidationError`) wrapped with `%w`; handlers map them to HTTP
  status codes in one place (`api/handlers` error mapping).
- All handlers use the JSON envelope helpers in `api/handlers/response.go`
  (`respondJSON`, `respondError`) — never write `json.NewEncoder` ad hoc.
- Use `slog` (structured logging) via the request-scoped logger set by
  middleware; don't use the global logger inside handlers.
- Every handler function takes `context.Context` end-to-end for cancellation.

### TypeScript / React
- Strict TypeScript; no `any` (see `tsconfig.json`).
- API access only through modules in `src/api/` — components never call
  `fetch` directly.
- Feature folders own their pages/containers; cross-feature reuse goes in
  `components/ui`.
- Server data types live in `src/types/domain.ts` and must mirror the backend
  DTO field names exactly (snake_case on the wire).

### Database
- SQLite runs in WAL mode with `busy_timeout` and `foreign_keys=ON`
  (set via DSN pragmas in `repository/db.go`).
- Migrations are embedded (`//go:embed migrations/*.sql`) and applied
  sequentially in one transaction each at startup.

## Environment / Configuration

All configuration is env-driven (`internal/config`):

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | `development` / `production` |
| `PORT` | `8080` | backend listen port (local dev uses 5001 via `.env`) |
| `DB_PATH` | `./data/finance.db` | SQLite file location |
| `BILLS_PATH` | `./data/bills` | uploaded receipt image storage |
| `STORES_PATH` | `./data/stores` | uploaded store logo storage |
| `CORS_ALLOWED_ORIGINS` | *(empty)* | comma-separated; empty = same-origin only |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `AI_ENCRYPTION_KEY` | *(empty)* | AES-256-GCM passphrase for AI provider keys; required in production |
| `LLM_TIMEOUT` | `5m` | per-extraction timeout for AI bill scanning (large local vision models need minutes) |

## Current State

Accounts, categories, stores, transactions, budgets, the planning dashboard (daily
expenses, budget progress, per-product-category spending), and AI bill
scanning (upload → extract → review → confirm, saved-bill editing, stats) are
implemented. Bills link to stores via `store_id`; on confirm/update the service
find-or-creates the store from the (case-insensitive) market name, while
`market_name` stays a denormalized snapshot. Receipt uploads are deduplicated by
content: each upload's sha256 is stored on `bill_scans` and `bills`, and
re-uploading the same image is a 409 conflict. Negative item prices are allowed
only for "Leergut" lines or items under a category with `allows_negative` (the
seeded "Deposit & Returns" product category covers Pfand/Leergut). When adding
a new entity, follow the vertical slice:
migration → domain model → repository → service → handler → route → frontend
api module → feature page.