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
| `PRODUCTS_PATH` | `./data/products` | uploaded product photo storage |
| `CORS_ALLOWED_ORIGINS` | *(empty)* | comma-separated; empty = same-origin only |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_FILE` | `./data/server.log` | log append target (records also go to stdout); `none` = stdout only |
| `AI_ENCRYPTION_KEY` | *(empty)* | AES-256-GCM passphrase for AI provider keys; required in production |
| `LLM_TIMEOUT` | `5m` | per-extraction timeout for AI bill scanning (large local vision models need minutes) |
| `LLM_NUM_CTX` | `0` | Ollama context-window override (`options.num_ctx`) for bill reads; `0` = model default — raise it when a local vision model's default window truncates big receipts mid-JSON |
| `FX_TIMEOUT` | `10s` | outbound timeout for the exchange-rates API (Frankfurter/ECB), cached 24h |

## Current State

Accounts, categories, stores, products, transactions, budgets, the planning
dashboard (daily expenses, budget progress, per-product-category spending), and
AI bill scanning (upload → extract → review → confirm, saved-bill editing,
stats) are implemented. Bills link to stores via `store_id`; on confirm/update
the service find-or-creates the store from the (case-insensitive) market name,
while `market_name` stays a denormalized snapshot. Bill items link to products
via `product_id`: on confirm the service find-or-creates the product from the
case-insensitive item name (deposit returns are never linked). Linked
bill/transaction lines are snapshots — they keep the name/unit/category they
were created with; product edits update the `products` row only (the merge
flow is the one exception, rewriting the items it redirects). Renaming a
product to a name that already exists offers a **merge** instead of failing on
the unique name index: `GET /products/{id}/merge-check` returns the matched
product and the plan (bought at different stores → simple confirm; bought at a
shared store → comparison dialog where the user picks which record to keep),
and `POST /products/{id}/merge` redirects the loser's bill items to the keeper
(purchase history — prices included — combines under it) and deletes the loser
row in one transaction. Products cannot
be created or deleted through the API; the two paginated endpoints are
`GET /api/v1/products` (flat list) and `GET /api/v1/products/grouped`
(the products view grouped by generic product family:
`{items, total, limit, offset}` envelope, sort key whitelist). The grouped
listing folds the catalogue in Go on top of one store-aware stats read —
one row per generic name, members embedded with their latest price and last
store (`last_store_name`), products with no family grouped under their raw
name; the name filter and sorting work on the group, the price columns are
family aggregates (average/best of the members' own averages in the family's
most recent purchase currency), and the category filter keeps whole groups
(any member matches). The group-level edit only changes the generic name:
the frontend sends one unchanged-field PUT per member with the new
`generic_name`, which learns it into every member's normalization mapping
(source user) — membership itself is decided by the mappings, never by the
edit. A receipt too long for one photo can be uploaded as **several
files in one scan**: `POST /bills/scan` accepts repeated `image` multipart
parts (1 = unchanged single receipt; 2..`MaxBillScanFiles`=8 = one receipt
split across consecutive parts, merged into ONE bill by the AI read — the
"parts of one receipt" checkbox in the scan UI is purely the grouped-vs-
per-file affordance). Every part is stored in the `bill_scan_files` /
`bill_files` child tables (position 1..n; the `bill_scans`/`bills` single-file
columns stay a denormalized mirror of part 1 for `GET /bills/image/{id}`,
which serves part N via `?part=N`), the AI payload carries all parts per
family (Ollama `images` array, OpenAI `image_url` parts, Gemini
`inline_data` parts, Anthropic image/document blocks), and
`file_count` (omitted for 1) is exposed on scans and bills. Receipt uploads
are deduplicated by
content: each part's sha256 lives in the child tables (backfilled from the
legacy columns), and re-uploading any part of an existing receipt is a 409
conflict naming the offending part — the same photo twice within one upload
is a 400. An in-progress analysis can be **cancelled**
(`POST /bills/scan/{token}/cancel`, fourth scan status `cancelled` from
migration 0029): the row leaves `analyzing` before the worker's result write
(all result writes are guarded on `status = 'analyzing'`, so a cancelled scan
is never resurrected) and the extraction is truly aborted — the worker
registers a per-scan `context.CancelFunc` the cancel signals, so the model
call stops; the kept photos are re-uploadable (dedup ignores failed AND
cancelled), the request stays deletable (`DELETE /bills/scan/{token}`)
or re-readable (`POST /extract` claims `cancelled` too), and cancelled rows
sweep with the session TTL. Negative item prices are allowed
only for "Leergut" lines or items under a category with `allows_negative` (the
seeded "Deposit & Returns" product category covers Pfand/Leergut) — the same
rule applies to manual transaction item lines, whose money-back lines also stay
unlinked from products. **Deposit-artifact lines are never products**: the
shared `domain.IsDepositArtifact` predicate (regex over `pfand`/`leergut`
unanchored and `mehrweg`/`einweg`/`deposit`/`gratis` word-bounded — glued
compounds like "Einwegpfand" match, "Einwegkamera"/"Gratissauce" don't) guards
every confirm path, so deposit charges (Pfand, Mehrweg, Einweg, "Bottle
deposit") and bare "Gratis" markers keep their real money in the bill or
transaction (charge positive under "Deposit & Returns", a Gratis line's
product belongs at price 0 per the prompts) but never link to a product or
enter the `product_name_mappings` memory on future confirms — existing data
is deliberately left untouched (no cleanup job; user decision), and migration
0030 refreshed the `bill_extraction` and `product_normalization` prompt seeds
(byte-guarded, custom prompts survive) to ask the AI for the same behavior.
The bill total is always recomputed as the sum of the
line totals **minus the bill-level global discount** (`discount_cents`, the
receipt-wide rebate printed after the article lines, e.g. "10% Rabatt" —
per-line discounts are already inside the lines), so discounted receipts
reconcile with their printed amount; VAT stays informational (already included
in the prices). In the review editor, only the deposit/refund **unit
price** carries a ± sign-flip button (phone decimal keypads have no minus
key) — discounts are positive by nature and never get one. The per-item
Category sits with the product name fields (it is what marks a line "Deposit
& Returns" and thereby allows the negative), and the Article field is a
product autocomplete: typing suggests catalogue products, picking one fills
the standardized/generic name, brand, measure and category (never the
prices, which come from the receipt), free entry is find-or-created on
confirm, and leaving a freely typed name looks up the name-mapping memory to
fill empty standardized/generic names and an unset category (deposit/return
lines are never looked up). The **purchase cart** keeps its items client-side only
(React Context + `localStorage` under `hfp.cart.v1`); confirming it starts a
persisted **offer search** (`offer_searches` table, token-polling pipeline like
bill scans): the connector flagged **default for web search** is asked for
current prices of the cart products in the user's local markets. AI connectors
carry per-purpose defaults — `default_for_bills` (bill reads) and
`default_for_search` (offer searches), stored on the `ai_providers` settings
row; a purpose with no flagged connector falls back to any configured one, and
legacy single `is_default` lists map onto both flags until the next save.
Connector families with native web search
(Gemini `google_search`, Anthropic `web_search`, OpenAI Responses API `web_search`,
and the `ollama_web_search` connector type — key-required — whose backend runs
Ollama's client-side `web_search`/`web_fetch` tool loop against ollama.com)
get the search tool attached; plain `ollama`/`openai_compatible` are prompted
only and any failure there becomes an actionable "configure a connector with
web search" error. The search scope is chosen in a pre-search options panel:
pinned stores (one offer entry per pinned market per product, otherwise any
local market) and a name match — `strict` (exact product names, the default)
or `loose` (include similar names/varieties, e.g. avocado → Hass, XL,
ready-to-eat, with each offer naming the variety actually sold). The scope
rides in `offer_searches.request_json` (`OfferSearchRequest` snapshot with a
legacy bare-array fallback decode), so retries reuse it. When stores are
pinned, stores without the product or without online prices are discriminated
with explicit rows: `availability` is `available` (priced, the default for
legacy rows) / `not_available` / `not_published` (priceless, muted in the UI,
never flagged best/worst). The normalized result is stored as JSON and rendered per product with
backend-computed `best_price` (green) / `worst_price` (red) flags per product and
currency; only failed searches are TTL-swept (done rows are the kept record).
The **AI prompts are database-driven**: the `ai_prompts` table (key, name,
description, content) holds the instruction texts, resolved by key —
`bill_extraction` (every receipt read; its `{{categories}}` placeholder is
expanded at run time from the live product-kind categories, so the taxonomy
in the prompt can no longer drift from the `categories` table) and
`offer_search` (the search head; product lines, scope and name-match mode are
appended in code). They are managed from `/settings/prompts` (full CRUD,
"reset to default" via the `default_content`/`uses_default` view fields), with
the built-in fallbacks in `internal/service/prompt_defaults.go` — an empty or
deleted row never breaks a process. Prompt keys are immutable after create
(processes resolve by key); the migration seed and the Go fallbacks are guarded
byte-identical by a repository seed-sync test. **Product name normalization**
keeps every raw product text untouched — product rows and bill lines keep
exactly what the receipt printed — and maps raw texts to standardized,
human-readable names plus a generic product family through the
`product_name_mappings` memory (raw_name UNIQUE NOCASE → standard_name +
generic_name + category, source `ai`/`user`/`manual`;
several raw texts may share one standard name). The `generic_name` is the
broader family across brands and sizes ("Potato Minions 450g" → "Frozen
Shaped Potatoes"); empty means "no broader family known" — it never falls
back to the raw text. Both AI prompt texts ask for the two names **always in
English**, regardless of the receipt/input language. The `bill_extraction`
prompt asks for a per-item `standard_name` and `generic_name` in the same
extraction call (`ParseBillJSON` falls back to the raw name only for the
standard name, keeping custom prompts working);
on extraction the memory wins (mapped raw texts reuse their remembered name,
family and category — a blank remembered family keeps the fresh suggestion —
and unmapped ones record the AI suggestion as `ai`), and
confirm/update learn real per-line edits permanently (upsert as `user`) —
deposit/return lines are never normalized or remembered. Manual purchases look
the memory up per typed line (category filled only when unset — explicit >
mapped > the transaction's main category, with that transaction-level default
never learned into the memory — typed name never rewritten; unknown names
record identity mappings as `manual`). Product resolution on both bill confirm
and manual saves is **mapping-first**: the memory's `product_id` link is
consulted before find-or-create by raw name, so a printed/typed name whose
standard form differs joins the mapped product's purchase history instead of
duplicating it (a stale link falls through and gets re-pointed by the save).
Blank manual `unit`/`unit_value` inherit the linked product's descriptors;
entered-but-unsanitary values stay the sanitized "unknown". A manual expense
with an open `account_id` records on the first listed account (error only when
no accounts exist). Product
edits learn the mapping too (`standard_name`/`generic_name` on
`PUT /products/{id}`, source
`user`): untouched saves write nothing, renames carry a mapped decision to
the new raw name (an unmapped rename stays open for AI suggestions), a
cleared field records identity, and an untouched `generic_name` is carried
through rather than wiped. The
standardized and generic names are exposed read-only (`standard_name`,
`generic_name`) on bill items,
transaction items and products via LEFT JOIN on the raw name. The frontend
uses `GET /products/normalize?name=` for the blur lookup; the user-triggered
backfill job (`POST /products/normalization/run`, status on
`GET /products/normalization`, single-row `product_normalization_jobs`) sends
product names with no mapping — or a mapping with no recorded family — to the
`default_for_bills` connector in batches of 40
under the `product_normalization` prompt key (a batch that outlives the
per-call `LLM_TIMEOUT` — slow local models — is deferred, not retried
immediately: the model server keeps generating the abandoned request, so an
instant retry of any size queues behind it and times out too — its names are
re-asked at the halved size once the rest of the run has drained the queue,
a timeout on a single-name batch fails the run with actionable advice, and
the working batch size is remembered for the process); for
existing mappings it only gap-fills the family, preserving the reviewed
standard name/category/source —
products are never modified,
names the AI skips are asked once per run, a failed batch no longer kills the
run (the job drains the rest and ends failed with its progress kept — re-run
retries the failed batch's names), and a job interrupted by a restart
is marked failed at boot and can simply be run again. **Unit prices** compute
per canonical unit from the printed size magnitude `unit_value` (REAL,
nullable — "500" for a "500ml" bottle, the number without its unit text,
migration 0027; older rows keep NULL and there is no backfill): the
`bill_extraction` schema asks the AI for it alongside `unit`, confirms seed
it onto new products and learn it into existing ones whose magnitude is
still unknown (a decided value is never overwritten; lines under 0/blank are
"unknown"), manual transaction lines carry it through the same learn flow,
and product edits are three-state (omitted = untouched, ≤ 0 = cleared,
> 0 = set) so untouched saves never erase learned magnitudes. The per-item
analytics price (g/ml ×1000 → kg/l) prefers `unit_price_cents / unit_value`
when the magnitude is known and falls back to the raw printed-unit formula
otherwise, the details modal shows "price per unit" from the average price ÷
magnitude, and `product_name_mappings` gained an opportunistic `product_id`
convenience link (FK `ON DELETE SET NULL`, backfilled where the raw text
NOCASE-matches a product name; all mapping lookups stay name-based).
**Background jobs** run through the River job queue
(`riverqueue.com/river` v0.48 with the `riversqlite` driver over the same
SQLite file): `internal/jobs/manager.go` owns a dedicated
single-connection pool (`_txlock=immediate` in the DSN) plus the queue's own
`rivermigrate` schema, `river.NewClient` with a 2-worker default queue, a
**disabled `JobTimeout`** (`-1`: River's built-in 1-minute default cancels a
job's context after 60s — on data-heavy installs the backfill's AI batch was
still generating when it fired and every retry died with "context deadline
exceeded"; workers bound their own per-call AI budgets with `LLM_TIMEOUT`, and
shutdown still cancels jobs via `StopAndCancel`) and a
**LLM-aware retry policy** — the first retry waits 2× `LLM_TIMEOUT` (floor
30s, doubling per attempt, capped 24h) so a retry never lands while a slow
local model is still generating the abandoned call, the lesson the
normalization job's instant-retry timeouts taught. Boot wiring lives in
`cmd/server/main.go` (`startJobs`): a failing queue only logs and boots
without jobs; shutdown drains with `Stop` then hard-cancels with
`StopAndCancel`. The **one-time unit-value backfill job**
(`internal/jobs/unit_value.go`, kind `unit_value_backfill`) fills migration
0027's `unit_value` columns: boot enqueues it (unless the
`unit_value_backfill_done` settings marker exists) and the worker parses the
printed size out of every stored raw name in passes (product names first,
bill/transaction lines copy their linked product's magnitude or parse their
own name last), then asks the `default_for_bills` connector — via the
`unit_value_backfill` prompt key (migration 0028 + `prompt_defaults.go`
fallback) — for the rows neither parser could resolve, in batches of 20
under a per-call `LLM_TIMEOUT` with an asked-set so unresolvable names never
spin the pass. Deposit/Leergut rows (allows_negative category or name) are
excluded in SQL, every write is guarded by `unit_value IS NULL` (retried runs
only work on the remainder, magnitudes never overwrite decided ones), a unit
is adopted only into an empty one (same dimension only), decimal commas and
German thousands dots ("1.000 g" → 1000, volumes stay decimal) are parsed
right-to-left with cl/dl scaled into ml, and success always writes the
completion marker even when rows stay unknown (an unreachable name is not an
error; a failing AI call is — River retries with the backoff). The
normalization-gap job (gpt above) stays on its in-process hand-rolled runner
for now and will be migrated onto River later. The queue is manageable through
the **River web UI embedded at `/riverui`** (OSS `riverqueue.com/riverui`
compiled into the backend binary — no extra process, no auth, the same
LAN-host posture as the API; retry/cancel/delete and job details live there):
reachable directly on the backend port and proxied through nginx in
`frontend/nginx.conf` (dev vite proxies `/riverui` too). Cross-links:
the sidebar's "Job queue" entry is a plain anchor (River lives outside the
SPA router), and `backend/internal/api/middleware/riverui_banner.go` injects
a "← Back to planner" pill into River's SPA-shell `text/html` response
(buffered only for browser-navigations whose Accept asks for text/html;
Content-Length is deleted before WriteHeader or the longer body truncates).
When adding
a new entity, follow the vertical slice:
migration → domain model → repository → service → handler → route → frontend
api module → feature page.
migration → domain model → repository → service → handler → route → frontend
api module → feature page.