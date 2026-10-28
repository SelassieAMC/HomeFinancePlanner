// Package api wires the HTTP routes and middleware chain.
package api

import (
	"log/slog"
	"net/http"

	"home-finance-planner/backend/internal/api/handlers"
	"home-finance-planner/backend/internal/api/middleware"
	"home-finance-planner/backend/internal/config"
	"home-finance-planner/backend/internal/service"
)

// NewRouter builds the full HTTP handler: middleware chain + route table.
// ui, when non-nil, is an additional http.Handler mounted outside the /api
// space (the River job-queue web UI at /riverui).
func NewRouter(cfg config.Config, log *slog.Logger, svc *service.Services, ui http.Handler) http.Handler {
	mux := http.NewServeMux()

	if ui != nil {
		// Session-guarded (it can retry/cancel jobs, so it stays behind the
		// household login) and wrapped in a middleware that injects the
		// "back to the planner" link into River's SPA shell (its only
		// text/html response); the UI itself strips the /riverui prefix.
		riverUI := middleware.RiverUIBackLink(ui)
		mux.Handle("GET /riverui", middleware.RequireSession(svc.Users.Current, riverUI))
		mux.Handle("/riverui/", middleware.RequireSession(svc.Users.Current, riverUI))
	}

	healthH := handlers.NewHealthHandler(cfg)
	accountH := &handlers.AccountHandler{Svc: svc.Accounts}
	categoryH := &handlers.CategoryHandler{Svc: svc.Categories}
	transactionH := &handlers.TransactionHandler{Svc: svc.Transactions}
	budgetH := &handlers.BudgetHandler{Svc: svc.Budgets}
	summaryH := &handlers.SummaryHandler{Svc: svc.Summary}
	billH := &handlers.BillHandler{Svc: svc.Bills}
	storeH := &handlers.StoreHandler{Svc: svc.Stores}
	productH := &handlers.ProductHandler{Svc: svc.Products}
	settingsH := &handlers.SettingsHandler{Svc: svc.Settings}
	analyticsH := &handlers.AnalyticsHandler{Svc: svc.Analytics}
	cartSearchH := &handlers.OfferSearchHandler{Svc: svc.OfferSearches}
	promptH := &handlers.PromptHandler{Svc: svc.Prompts}
	insightH := &handlers.InsightHandler{Svc: svc.Insights}
	goalH := &handlers.GoalHandler{Svc: svc.Goals}
	bagH := &handlers.BagHandler{Svc: svc.Bags}
	scheduleH := &handlers.ScheduleHandler{Svc: svc.Schedules}
	authH := &handlers.AuthHandler{Svc: svc.Users}
	meH := &handlers.MeHandler{Svc: svc.Users}
	adminUserH := &handlers.AdminUserHandler{Svc: svc.Users}
	fxH := &handlers.FxHandler{Svc: svc.FX}

	// Route table. Patterns are method-aware (Go 1.22+ ServeMux).
	mux.HandleFunc("GET /api/v1/health", healthH.Check)

	// Auth: the login page (public — the user dropdown plus the login
	// attempt) and the session lifecycle. Everything else under /api sits
	// behind middleware.Auth below.
	mux.HandleFunc("GET  /api/v1/auth/login-users", authH.LoginUsers)
	mux.HandleFunc("POST /api/v1/auth/login", authH.Login)
	mux.HandleFunc("POST /api/v1/auth/logout", authH.Logout)
	mux.HandleFunc("GET  /api/v1/auth/me", authH.Me)

	// Own-account profile (settings page): display name, password, avatar.
	mux.HandleFunc("PUT    /api/v1/me", meH.Update)
	mux.HandleFunc("PUT    /api/v1/me/locale", meH.UpdateLocale)
	mux.HandleFunc("PUT    /api/v1/me/password", meH.ChangePassword)
	mux.HandleFunc("POST   /api/v1/me/photo", meH.UploadPhoto)
	mux.HandleFunc("GET    /api/v1/me/photo", meH.Photo)
	mux.HandleFunc("DELETE /api/v1/me/photo", meH.RemovePhoto)

	// Admin user management: /users/{id}/photo is a 4-segment literal-free
	// route next to nothing — /api/v1/users has no other {id} wildcards.
	mux.HandleFunc("GET    /api/v1/users", adminUserH.List)
	mux.HandleFunc("POST   /api/v1/users", adminUserH.Create)
	mux.HandleFunc("PUT    /api/v1/users/{id}", adminUserH.Update)
	mux.HandleFunc("DELETE /api/v1/users/{id}", adminUserH.Delete)
	mux.HandleFunc("GET    /api/v1/users/{id}/photo", adminUserH.Photo)

	mux.HandleFunc("GET /api/v1/accounts", accountH.List)
	mux.HandleFunc("POST /api/v1/accounts", accountH.Create)
	mux.HandleFunc("GET /api/v1/accounts/{id}", accountH.Get)
	mux.HandleFunc("PUT /api/v1/accounts/{id}", accountH.Update)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}", accountH.Delete)
	mux.HandleFunc("POST /api/v1/accounts/{id}/reconcile", accountH.Reconcile)
	mux.HandleFunc("GET /api/v1/accounts/{id}/ledger", accountH.Ledger)

	mux.HandleFunc("GET /api/v1/categories", categoryH.List)
	mux.HandleFunc("POST /api/v1/categories", categoryH.Create)
	mux.HandleFunc("DELETE /api/v1/categories/{id}", categoryH.Delete)

	mux.HandleFunc("GET /api/v1/transactions", transactionH.List)
	mux.HandleFunc("POST /api/v1/transactions", transactionH.Create)
	mux.HandleFunc("GET /api/v1/transactions/{id}", transactionH.Get)
	mux.HandleFunc("PUT /api/v1/transactions/{id}", transactionH.Update)
	mux.HandleFunc("DELETE /api/v1/transactions/{id}", transactionH.Delete)

	mux.HandleFunc("GET /api/v1/budgets", budgetH.List)
	mux.HandleFunc("POST /api/v1/budgets", budgetH.Create)
	mux.HandleFunc("PUT /api/v1/budgets/{id}", budgetH.Update)
	mux.HandleFunc("PUT /api/v1/budgets/{id}/status", budgetH.SetStatus)
	mux.HandleFunc("DELETE /api/v1/budgets/{id}", budgetH.Delete)

	mux.HandleFunc("GET /api/v1/summary", summaryH.Get)
	// Literal route next to the exact-match /summary — no wildcard interactions.
	mux.HandleFunc("GET /api/v1/summary/glance", summaryH.Glance)

	// Analytics: dashboard chart aggregates. All literal routes — no wildcard
	// interactions with the {id} routes above.
	mux.HandleFunc("GET /api/v1/analytics/store-price-index", analyticsH.StorePriceIndex)
	mux.HandleFunc("GET /api/v1/analytics/category-sunburst", analyticsH.CategorySunburst)
	mux.HandleFunc("GET /api/v1/analytics/run-rate", analyticsH.RunRate)
	mux.HandleFunc("GET /api/v1/analytics/price-index", analyticsH.PriceIndex)
	mux.HandleFunc("GET /api/v1/analytics/spend-heatmap", analyticsH.SpendHeatmap)
	mux.HandleFunc("GET /api/v1/analytics/fixed-split", analyticsH.FixedSplit)
	mux.HandleFunc("GET /api/v1/analytics/top-products", analyticsH.TopProducts)
	mux.HandleFunc("GET /api/v1/analytics/top-stores", analyticsH.TopStores)

	// Scan flow: drafts live in bill_scans until the user confirms or discards.
	// POST /scan returns immediately with status "analyzing"; the client polls
	// GET /scan/{token}. (Literals beat the {id} wildcard in ServeMux.)
	mux.HandleFunc("POST /api/v1/bills/scan", billH.Scan)
	mux.HandleFunc("GET /api/v1/bills/scans", billH.ListScans)
	mux.HandleFunc("GET /api/v1/bills/scan/{token}", billH.GetScan)
	mux.HandleFunc("POST /api/v1/bills/scan/{token}/extract", billH.Reextract)
	mux.HandleFunc("POST /api/v1/bills/scan/{token}/confirm", billH.Confirm)
	mux.HandleFunc("POST /api/v1/bills/scan/{token}/cancel", billH.CancelScan)
	mux.HandleFunc("GET /api/v1/bills/scan/{token}/image", billH.ScanImage)
	mux.HandleFunc("DELETE /api/v1/bills/scan/{token}", billH.DiscardScan)
	mux.HandleFunc("GET /api/v1/bills", billH.List)
	mux.HandleFunc("GET /api/v1/bills/{id}", billH.Get)
	mux.HandleFunc("POST /api/v1/bills/{id}/re-extract", billH.Rescan)
	mux.HandleFunc("PUT /api/v1/bills/{id}", billH.Update)
	mux.HandleFunc("DELETE /api/v1/bills/{id}", billH.Delete)
	// /image/{id} rather than /{id}/image: a 3-segment wildcard route next to
	// /bills/scan/{token} (e.g. /{id}/image) is ambiguous with it in ServeMux
	// (".../scan/image" matches both, neither more specific).
	mux.HandleFunc("GET /api/v1/bills/image/{id}", billH.Image)
	mux.HandleFunc("GET /api/v1/bills/brands", billH.Brands)

	// Stores: recurring markets a bill links to (find-or-created from the
	// market name on confirm). /{id}/logo is 4 segments next to a 2-segment
	// /{id} — unambiguous in ServeMux. Never add a 3-segment store wildcard
	// here (see the /bills note).
	mux.HandleFunc("GET /api/v1/stores", storeH.List)
	mux.HandleFunc("POST /api/v1/stores", storeH.Create)
	mux.HandleFunc("GET /api/v1/stores/{id}", storeH.Get)
	mux.HandleFunc("PUT /api/v1/stores/{id}", storeH.Update)
	mux.HandleFunc("DELETE /api/v1/stores/{id}", storeH.Delete)
	mux.HandleFunc("POST /api/v1/stores/{id}/logo", storeH.UploadLogo)
	mux.HandleFunc("GET /api/v1/stores/{id}/logo", storeH.Logo)
	mux.HandleFunc("DELETE /api/v1/stores/{id}/logo", storeH.RemoveLogo)

	// Products: the catalogue of things ever bought (find-or-created from bill
	// items on confirm; the UI only edits, never creates or deletes).
	// /{id}/photo is 4 segments next to a 2-segment /{id} — unambiguous, like
	// the store logo routes.
	mux.HandleFunc("GET /api/v1/products", productH.List)
	mux.HandleFunc("GET /api/v1/products/grouped", productH.ListGrouped)
	mux.HandleFunc("GET /api/v1/products/normalize", productH.NormalizeName)
	mux.HandleFunc("POST /api/v1/products/normalization/run", productH.RunNormalization)
	mux.HandleFunc("GET /api/v1/products/normalization", productH.NormalizationStatus)
	mux.HandleFunc("GET /api/v1/products/{id}", productH.Get)
	mux.HandleFunc("GET /api/v1/products/{id}/prices", productH.StorePrices)
	mux.HandleFunc("GET /api/v1/products/{id}/merge-check", productH.CheckMerge)
	mux.HandleFunc("POST /api/v1/products/{id}/merge", productH.Merge)
	mux.HandleFunc("PUT /api/v1/products/{id}", productH.Update)
	mux.HandleFunc("POST /api/v1/products/{id}/photo", productH.UploadPhoto)
	mux.HandleFunc("GET /api/v1/products/{id}/photo", productH.Photo)
	mux.HandleFunc("DELETE /api/v1/products/{id}/photo", productH.RemovePhoto)

	// Offer search flow (purchase cart): one row per confirmed cart, result
	// persisted in the row. POST returns immediately with status "searching";
	// the client polls GET /{token}. Self-contained family — no {id} wildcard
	// interactions.
	mux.HandleFunc("POST   /api/v1/cart-searches", cartSearchH.Create)
	mux.HandleFunc("GET    /api/v1/cart-searches", cartSearchH.List)
	mux.HandleFunc("GET    /api/v1/cart-searches/{token}", cartSearchH.Get)
	mux.HandleFunc("POST   /api/v1/cart-searches/{token}/retry", cartSearchH.Retry)
	mux.HandleFunc("DELETE /api/v1/cart-searches/{token}", cartSearchH.Delete)

	// Insights: the saved price-per-unit nudges of the deferred-intelligence
	// job. Self-contained family — no wildcard interactions.
	mux.HandleFunc("GET  /api/v1/insights", insightH.List)
	mux.HandleFunc("POST /api/v1/insights/{id}/dismiss", insightH.Dismiss)

	// Goals: savings targets tracked against one account. Self-contained
	// family — no wildcard interactions.
	mux.HandleFunc("GET /api/v1/goals", goalH.List)
	mux.HandleFunc("POST /api/v1/goals", goalH.Create)
	mux.HandleFunc("PUT /api/v1/goals/{id}", goalH.Update)
	mux.HandleFunc("DELETE /api/v1/goals/{id}", goalH.Delete)

	// Bags: allocation envelopes inside one account. /year-close and
	// /rebalance are literals next to the {id} wildcard — unambiguous in
	// ServeMux. The {id}/allocations routes are 4 segments next to the
	// 2-segment /{id} — unambiguous.
	mux.HandleFunc("GET    /api/v1/bags", bagH.List)
	mux.HandleFunc("POST   /api/v1/bags", bagH.Create)
	mux.HandleFunc("GET    /api/v1/bags/year-close", bagH.YearClose)
	mux.HandleFunc("POST   /api/v1/bags/rebalance", bagH.Rebalance)
	mux.HandleFunc("GET    /api/v1/bags/{id}", bagH.Get)
	mux.HandleFunc("PUT    /api/v1/bags/{id}", bagH.Update)
	mux.HandleFunc("PUT    /api/v1/bags/{id}/status", bagH.SetStatus)
	mux.HandleFunc("DELETE /api/v1/bags/{id}", bagH.Delete)
	mux.HandleFunc("GET    /api/v1/bags/{id}/allocations", bagH.ListAllocations)
	mux.HandleFunc("POST   /api/v1/bags/{id}/allocations", bagH.Allocate)

	// Scheduled actions: the unified scheduler. /runs is a literal next to the
	// {id} wildcard routes; the run action routes live one segment deeper
	// (/runs/{run_id}/apply), so the two id spaces never collide. Self-
	// contained family — no wildcard interactions.
	mux.HandleFunc("GET    /api/v1/scheduled-actions", scheduleH.List)
	mux.HandleFunc("POST   /api/v1/scheduled-actions", scheduleH.Create)
	mux.HandleFunc("GET    /api/v1/scheduled-actions/runs", scheduleH.ListRuns)
	mux.HandleFunc("PUT    /api/v1/scheduled-actions/{id}", scheduleH.Update)
	mux.HandleFunc("DELETE /api/v1/scheduled-actions/{id}", scheduleH.Delete)
	mux.HandleFunc("POST   /api/v1/scheduled-actions/{id}/archive", scheduleH.Archive)
	mux.HandleFunc("POST   /api/v1/scheduled-actions/runs/{run_id}/apply", scheduleH.ApplyRun)
	mux.HandleFunc("POST   /api/v1/scheduled-actions/runs/{run_id}/dismiss", scheduleH.DismissRun)
	mux.HandleFunc("POST   /api/v1/scheduled-actions/runs/{run_id}/skip", scheduleH.SkipRun)

	mux.HandleFunc("GET /api/v1/settings/ai", settingsH.ListAIProviders)
	mux.HandleFunc("PUT /api/v1/settings/ai", settingsH.SaveAIProviders)
	mux.HandleFunc("POST /api/v1/settings/ai/test/{id}", settingsH.TestAIProvider)

	// Managed AI prompt texts (bill extraction, offer search, custom).
	mux.HandleFunc("GET /api/v1/prompts", promptH.List)
	mux.HandleFunc("POST /api/v1/prompts", promptH.Create)
	mux.HandleFunc("GET /api/v1/prompts/{id}", promptH.Get)
	mux.HandleFunc("PUT /api/v1/prompts/{id}", promptH.Update)
	mux.HandleFunc("DELETE /api/v1/prompts/{id}", promptH.Delete)
	mux.HandleFunc("GET /api/v1/settings/currency", settingsH.GetBaseCurrency)
	mux.HandleFunc("PUT /api/v1/settings/currency", settingsH.SaveBaseCurrency)

	// Public exchange-rate snapshot (Frankfurter/ECB, cached 24h): the
	// reference rate the transfer form and bill review show before the user
	// overrides it with the bank's own rate. Read-only.
	mux.HandleFunc("GET /api/v1/fx/rates", fxH.Rates)

	// Middleware chain, outermost first. Auth sits inside CORS (preflight
	// answers never need a session) and around every route: /api requires a
	// login cookie except the public login/health routes above; the /riverui
	// mounts carry their own session guard; anything else outside /api
	// passes through.
	return middleware.RequestID(
		middleware.Logger(log,
			middleware.Recover(
				middleware.AccessLog(
					middleware.CORS(cfg.CORSAllowedOrigins,
						middleware.Auth(svc.Users.Current, mux)),
				),
			),
		),
	)
}
