.PHONY: help dev dev-backend dev-frontend test-backend lint-backend typecheck-frontend \
        lint-frontend build up down logs clean

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# --- Local development -------------------------------------------------------

dev: ## Run backend (:5001) + frontend (:5002) concurrently
	$(MAKE) -j2 dev-backend dev-frontend

dev-backend: ## Run backend, loading backend/.env first (listens on :5001)
	cd backend && set -a && . ./.env && set +a && exec go run ./cmd/server

dev-frontend: ## Run Vite dev server
	cd frontend && npm run dev -- --host

# --- Quality -----------------------------------------------------------------

test-backend: ## Run backend unit tests
	cd backend && go test ./... -race -count=1

lint-backend: ## Vet + format check backend
	cd backend && go vet ./... && test -z "$$(gofmt -l .)"

lint-frontend: ## ESLint frontend
	cd frontend && npm run lint

typecheck-frontend: ## TypeScript strict check frontend
	cd frontend && npm run typecheck

# --- Docker ------------------------------------------------------------------

build: ## Build both images
	docker compose build

up: ## Start full stack
	docker compose up -d

down: ## Stop full stack
	docker compose down

logs: ## Tail stack logs
	docker compose logs -f

clean: ## Remove build artifacts and local db
	rm -rf backend/bin frontend/dist data
