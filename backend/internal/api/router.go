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
func NewRouter(cfg config.Config, log *slog.Logger, svc *service.Services) http.Handler {
	mux := http.NewServeMux()

	healthH := handlers.NewHealthHandler(cfg)
	accountH := &handlers.AccountHandler{Svc: svc.Accounts}
	categoryH := &handlers.CategoryHandler{Svc: svc.Categories}
	transactionH := &handlers.TransactionHandler{Svc: svc.Transactions}
	budgetH := &handlers.BudgetHandler{Svc: svc.Budgets}
	summaryH := &handlers.SummaryHandler{Svc: svc.Summary}
	billH := &handlers.BillHandler{Svc: svc.Bills}
	storeH := &handlers.StoreHandler{Svc: svc.Stores}
	settingsH := &handlers.SettingsHandler{Svc: svc.Settings}

	// Route table. Patterns are method-aware (Go 1.22+ ServeMux).
	mux.HandleFunc("GET /api/v1/health", healthH.Check)

	mux.HandleFunc("GET /api/v1/accounts", accountH.List)
	mux.HandleFunc("POST /api/v1/accounts", accountH.Create)
	mux.HandleFunc("GET /api/v1/accounts/{id}", accountH.Get)
	mux.HandleFunc("PUT /api/v1/accounts/{id}", accountH.Update)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}", accountH.Delete)

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
	mux.HandleFunc("DELETE /api/v1/budgets/{id}", budgetH.Delete)

	mux.HandleFunc("GET /api/v1/summary", summaryH.Get)

	// Scan flow: drafts live in bill_scans until the user confirms or discards.
	// POST /scan returns immediately with status "analyzing"; the client polls
	// GET /scan/{token}. (Literals beat the {id} wildcard in ServeMux.)
	mux.HandleFunc("POST /api/v1/bills/scan", billH.Scan)
	mux.HandleFunc("GET /api/v1/bills/scans", billH.ListScans)
	mux.HandleFunc("GET /api/v1/bills/scan/{token}", billH.GetScan)
	mux.HandleFunc("POST /api/v1/bills/scan/{token}/extract", billH.Reextract)
	mux.HandleFunc("POST /api/v1/bills/scan/{token}/confirm", billH.Confirm)
	mux.HandleFunc("DELETE /api/v1/bills/scan/{token}", billH.DiscardScan)
	mux.HandleFunc("GET /api/v1/bills", billH.List)
	mux.HandleFunc("GET /api/v1/bills/{id}", billH.Get)
	mux.HandleFunc("PUT /api/v1/bills/{id}", billH.Update)
	mux.HandleFunc("DELETE /api/v1/bills/{id}", billH.Delete)
	// /image/{id} rather than /{id}/image: a 3-segment wildcard route next to
	// /bills/scan/{token} (e.g. /{id}/image) is ambiguous with it in ServeMux
	// (".../scan/image" matches both, neither more specific).
	mux.HandleFunc("GET /api/v1/bills/image/{id}", billH.Image)
	mux.HandleFunc("GET /api/v1/bills/stats", billH.Stats)
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

	mux.HandleFunc("GET /api/v1/settings/ai", settingsH.ListAIProviders)
	mux.HandleFunc("PUT /api/v1/settings/ai", settingsH.SaveAIProviders)
	mux.HandleFunc("POST /api/v1/settings/ai/test/{id}", settingsH.TestAIProvider)
	mux.HandleFunc("GET /api/v1/settings/currency", settingsH.GetBaseCurrency)
	mux.HandleFunc("PUT /api/v1/settings/currency", settingsH.SaveBaseCurrency)

	// Middleware chain, outermost first.
	return middleware.RequestID(
		middleware.Logger(log,
			middleware.Recover(
				middleware.AccessLog(
					middleware.CORS(cfg.CORSAllowedOrigins, mux),
				),
			),
		),
	)
}
