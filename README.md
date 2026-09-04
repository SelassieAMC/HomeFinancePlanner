# home-finance-planner

Self-hosted home finance planner: track accounts, transactions, categories,
and monthly budgets, scan market receipts with AI, and plan spending with a
dashboard.

## Stack

| Layer | Technology |
|---|---|
| Frontend | React 18, TypeScript, Vite |
| Backend | Go (stdlib `net/http`), layered architecture |
| Database | SQLite (WAL, embedded migrations) |
| Runtime | Docker / docker-compose |

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Go | 1.26+ | backend dev / building the Docker image |
| Node.js + npm | 18+ | frontend dev / typecheck / lint |
| Docker + Compose | recent | full-stack deployment (optional) |
| make | any | convenience targets |

## Configuration (`.env`)

All backend configuration is environment-driven (`backend/internal/config`).
The server reads **plain environment variables** — nothing loads a `.env`
file automatically, so export it before starting the backend.

Create a `.env` file at the repository root from the bundled example (it is
gitignored — keep secrets out of the repo):

```bash
cp .env.example .env    # then edit the values
```

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | `development` / `production` — production **requires** `AI_ENCRYPTION_KEY` |
| `PORT` | `8080` | backend HTTP port. Local dev uses **5001** — the Vite dev server proxies `/api` to `http://localhost:5001` |
| `DB_PATH` | `./data/finance.db` | SQLite file location (WAL mode; directory is created) |
| `BILLS_PATH` | `./data/bills` | where scanned receipt images are stored |
| `CORS_ALLOWED_ORIGINS` | *(empty)* | comma-separated origins allowed to call the API; empty = same-origin only |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `AI_ENCRYPTION_KEY` | *(empty)* | passphrase for AES-256-GCM encryption of AI provider API keys. In development an insecure fallback key is used (with a warning); in production a missing key is a startup **error** |
| `BACKEND_PORT` | `5001` | Docker host port for the API (container-internal stays 8080) |
| `UI_PORT` | `5002` | Docker host port for the web UI |

Notes:

- Money is stored as integer cents; dates are ISO strings; budget months are
  `YYYY-MM`.
- The SQLite database and migrations run automatically on first start — delete
  `backend/data/finance.db` to reset and re-seed the category taxonomy.

## Environments

### Local development

```bash
# create .env as shown above, then load it into your shell
set -a && source .env && set +a

# Terminal 1 — backend on :5001 (matches the Vite proxy target)
make dev-backend

# Terminal 2 — frontend on :5002 (Vite proxies /api → :5001)
make dev-frontend
```

The frontend dev server needs no CORS configuration: `frontend/vite.config.ts`
serves the SPA on `:5002` and proxies `/api` to `http://localhost:5001`, so
browser calls stay same-origin (same as the production nginx setup).

To test the AI bill-scanning flow you need at least one connector: start the
app, open **Settings**, and add a provider (Ollama local, OpenAI, Gemini,
Anthropic, or any OpenAI-compatible endpoint). Saved API keys are encrypted
with `AI_ENCRYPTION_KEY` — changing the passphrase afterwards makes stored
keys unreadable, so pick one and keep it.

Useful targets:

```bash
make test-backend          # Go tests
make lint-backend          # go vet + gofmt check
make typecheck-frontend    # tsc --noEmit
make lint-frontend         # eslint
```

### Docker / production

```bash
cp .env.example .env    # then edit: set AI_ENCRYPTION_KEY (required)
docker compose up -d    # or: make up
make logs
make down
```

With the default configuration the stack exposes:

| URL | What |
|---|---|
| `http://localhost:5002` | Web UI (nginx serving the SPA) |
| `http://localhost:5001` | Backend API (`/api/v1/…`, direct access) |

Port layout and wiring:

- Host ports come from the root `.env` (Compose reads it automatically):
  `BACKEND_PORT=5001` and `UI_PORT=5002`. Container-internal ports stay
  fixed: the backend always listens on `8080`, nginx on `80`.
- nginx inside the UI container proxies `/api` to `http://backend:8080`, so
  the browser only ever talks to `:5002` (same-origin — no CORS needed).
- `CORS_ALLOWED_ORIGINS` stays empty in Docker. Only set it if you deploy the
  frontend separately from the backend (e.g. `https://finance.example.com`).
- `AI_ENCRYPTION_KEY` is **required** — compose refuses to start without it.
  Generate one with `openssl rand -base64 32`. The SQLite database lives on
  the `sqlite-data` volume (survives `make down`, wiped by
  `docker compose down -v`).

Rebuild after code changes:

```bash
make build && make up
```

## API

All endpoints are versioned under `/api/v1`:

```
GET    /api/v1/health
GET    /api/v1/accounts            POST /api/v1/accounts
GET    /api/v1/accounts/{id}       PUT  /api/v1/accounts/{id}   DELETE /api/v1/accounts/{id}
GET    /api/v1/categories          POST /api/v1/categories
GET    /api/v1/transactions        POST /api/v1/transactions
GET    /api/v1/transactions/{id}   PUT  /api/v1/transactions/{id}   DELETE /api/v1/transactions/{id}
GET    /api/v1/budgets?month=YYYY-MM   POST /api/v1/budgets
PUT    /api/v1/budgets/{id}        DELETE /api/v1/budgets/{id}
GET    /api/v1/summary?month=YYYY-MM   # dashboard aggregates

# Bill scanning (AI receipt extraction)
POST   /api/v1/bills/scan                          # upload receipt photo/PDF
POST   /api/v1/bills/scan/{token}/extract          # (re-)run extraction
POST   /api/v1/bills/scan/{token}/confirm          # save + optional expense transaction
DELETE /api/v1/bills/scan/{token}                  # discard scan + receipt file
GET    /api/v1/bills?month=YYYY-MM&status=accepted
GET    /api/v1/bills/{id}       PUT /api/v1/bills/{id}
GET    /api/v1/bills/{id}/image
GET    /api/v1/bills/stats?group_by=market|month|week|item|category&month=
GET    /api/v1/bills/brands

# AI connectors
GET    /api/v1/settings/ai         PUT  /api/v1/settings/ai
POST   /api/v1/settings/ai/test/{id}
```

## Project layout & conventions

See [CLAUDE.md](CLAUDE.md) for the enforced architecture rules, folder
layout, and command reference.