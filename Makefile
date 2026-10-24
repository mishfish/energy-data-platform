GOLANGCI_LINT_VERSION := v2.14.0
# Override with a locally installed binary: make lint GOLANGCI_LINT=golangci-lint
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Keep in sync with the prometheus service in docker-compose.yml.
PROMETHEUS_IMAGE := prom/prometheus:v3.15.0

.PHONY: help build test test-dashboard test-int test-rules lint fmt tidy up up-fake fault fault-clear down down-fake ps logs migrate

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

build: ## Build all Go packages
	go build ./...

test: ## Unit tests with the race detector
	go test -race ./...

test-int: ## Integration tests (testcontainers, needs Docker)
	go test -race -tags=integration ./...

test-rules: ## Prometheus rule unit tests (promtool from the Prometheus image, needs Docker)
	docker run --rm -v "$(CURDIR)/observability/prometheus:/etc/prometheus:ro" --entrypoint promtool \
		$(PROMETHEUS_IMAGE) test rules /etc/prometheus/tests/ingest_test.yml /etc/prometheus/tests/cdc_test.yml /etc/prometheus/tests/sink_test.yml /etc/prometheus/tests/dashboard_test.yml

test-dashboard: ## Dashboard unit tests and ruff; page smoke tests run when ClickHouse is up (make up)
	cd analytics/dashboard && uv run ruff check . && uv run ruff format --check . && uv run pytest -q

lint: ## golangci-lint (includes gofmt/goimports checks)
	$(GOLANGCI_LINT) run ./...

fmt: ## Format Go code
	$(GOLANGCI_LINT) fmt ./...

tidy: ## go mod tidy
	go mod tidy

up: ## Start the local stack (rebuilds Go service images)
	docker compose up -d --build

# The fake stack is a separate compose project with its own volumes, so synthetic data never mixes
# with real data (ADR 0021). It uses the same host ports: stop one stack before starting the other.
# The shell variables override .env, so a real token in .env is not sent to the fake.
FAKE_COMPOSE := docker compose -p energy-data-platform-fake --profile fake

up-fake: ## Start a separate stack against the synthetic ENTSO-E API (no token needed)
	ENTSOE_BASE_URL=http://fake-entsoe:8080/api ENTSOE_API_TOKEN=fake $(FAKE_COMPOSE) up -d --build

down-fake: ## Stop the synthetic stack (keeps its volumes)
	$(FAKE_COMPOSE) down

FAKE_ENTSOE_URL ?= http://localhost:$${FAKE_ENTSOE_PORT:-8081}

fault: ## Inject a fake API failure: make fault f='{"status":503}' (also delay, retry_after)
	curl -fsS -X PUT -d '$(f)' $(FAKE_ENTSOE_URL)/admin/fault

fault-clear: ## Clear the injected fake API failure
	curl -fsS -X DELETE $(FAKE_ENTSOE_URL)/admin/fault

down: ## Stop the local stack (keeps volumes)
	docker compose down

migrate: ## Apply schema migrations via the one-shot compose service
	docker compose run --rm --build migrate

ps: ## Show stack status
	docker compose ps

logs: ## Follow logs: make logs s=<service>
	docker compose logs -f $(s)
