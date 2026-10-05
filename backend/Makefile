.PHONY: build test test-integration coverage fmt openapi

build:
	go build ./...

test:
	go test ./...

# Integration tests run against a real PostgreSQL, because what they check is
# which rows a WHERE clause selects — a mock replays the rows the test wrote and
# agrees with whatever the query says. They are skipped by `make test`, so this
# target starts a throwaway database, runs them, and removes it either way.
#
# -p 1 runs one package's tests at a time: every integration test truncates the
# tables it touches, and two packages truncating/seeding the same shared
# Postgres in parallel step on each other's rows (duplicate keys, rows vanishing
# mid-test). Each package's own tests may still run concurrently within it.
PG_ITEST_PORT ?= 55433
PG_ITEST_NAME ?= mm-itest
test-integration:
	@docker rm -f $(PG_ITEST_NAME) >/dev/null 2>&1 || true
	@docker run -d --name $(PG_ITEST_NAME) \
		-e POSTGRES_PASSWORD=postgres -e POSTGRES_USER=postgres -e POSTGRES_DB=middlemonitor_test \
		-p $(PG_ITEST_PORT):5432 postgres:16-alpine >/dev/null
	@until docker exec $(PG_ITEST_NAME) pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	@set -e; trap 'docker rm -f $(PG_ITEST_NAME) >/dev/null 2>&1 || true' EXIT; \
	MM_INTEGRATION_DB="postgres://postgres:postgres@localhost:$(PG_ITEST_PORT)/middlemonitor_test?sslmode=disable" \
	DB_HOST=localhost DB_PORT=$(PG_ITEST_PORT) DB_USER=postgres DB_PASSWORD=postgres \
	DB_NAME=middlemonitor_test DB_SSLMODE=disable \
	JWT_SECRET=integration-test-secret-at-least-32-chars \
	go test ./... -count=1 -p 1

# Prints the per-package coverage and the total, and writes cover.html for the
# line-by-line view. The README table is a snapshot of this output.
coverage:
	@go test ./... -coverprofile=cover.out -count=1 >/dev/null
	@go tool cover -func=cover.out | tail -1
	@go test ./... -cover -count=1 2>/dev/null | grep -E 'coverage: [0-9]' | grep -v 'coverage: 0.0%'
	@go tool cover -html=cover.out -o cover.html
	@echo "cover.html written"

fmt:
	gofmt -s -w .

# Regenerates api/openapi.json from the routes the router registers.
openapi:
	go run ./scripts/routes > /tmp/mm-routes.json
	node scripts/gen-openapi.mjs /tmp/mm-routes.json
