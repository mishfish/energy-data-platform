GOLANGCI_LINT_VERSION := v2.14.0
# Override with a locally installed binary: make lint GOLANGCI_LINT=golangci-lint
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: help build test test-int lint fmt tidy up down ps logs migrate

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

build: ## Build all Go packages
	go build ./...

test: ## Unit tests with the race detector
	go test -race ./...

test-int: ## Integration tests (testcontainers, needs Docker)
	go test -race -tags=integration ./...

lint: ## golangci-lint (includes gofmt/goimports checks)
	$(GOLANGCI_LINT) run ./...

fmt: ## Format Go code
	$(GOLANGCI_LINT) fmt ./...

tidy: ## go mod tidy
	go mod tidy

up: ## Start the local stack (rebuilds Go service images)
	docker compose up -d --build

down: ## Stop the local stack (keeps volumes)
	docker compose down

migrate: ## Apply schema migrations via the one-shot compose service
	docker compose run --rm --build migrate

ps: ## Show stack status
	docker compose ps

logs: ## Follow logs: make logs s=<service>
	docker compose logs -f $(s)
