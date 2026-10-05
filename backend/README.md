# Backend

Go services behind Middle Monitor. They share one module and one PostgreSQL schema.

| Command        | Role |
|----------------|------|
| `cmd/api`      | Dashboard and management API. Runs the database migrations at startup. |
| `cmd/receiver` | Ingestion: agents, SDKs, OTLP (`/v1/traces`, `/v1/logs`, `/v1/metrics`). Publishes to Kafka. |
| `cmd/worker`   | Runs the checks, consumes Kafka, evaluates alerts, applies retention. |
| `cmd/payments` | Stripe checkout and webhooks. Only used by the hosted service. |
| `cmd/metricseed` | Fills a local database with demo series. |

Traces, logs and metric series are stored in OpenSearch; everything else in PostgreSQL.

## Running locally

Needs PostgreSQL 16, OpenSearch 2 (security plugin disabled) and Kafka 3 on
localhost. Then:

```bash
export DB_HOST=localhost DB_USER=middlemonitor DB_PASSWORD=... DB_NAME=middlemonitor
export JWT_SECRET=$(openssl rand -base64 32)
export OPENSEARCH_URL=http://localhost:9200 KAFKA_BROKERS=localhost:9092
go run ./cmd/api        # :8080
PORT=8081 go run ./cmd/receiver
go run ./cmd/worker
```

`LOG_LEVEL` (`debug`, `info`, `warn`, `error`) sets the log level; logs are JSON on stderr.
The full list of settings is in `deploy/self-hosted/.env.example`.

The API describes itself at `GET /api/v1/openapi.json` (regenerate with `make openapi`).

## Tests

```bash
make test              # unit tests, integration tests are skipped
make test-integration  # starts a throwaway PostgreSQL and runs everything
make coverage          # per-package coverage and cover.html
```

Integration tests check what a SQL query actually selects, which a mock cannot
tell. They are skipped unless `MM_INTEGRATION_DB` is set, so `make test` stays
fast and needs nothing installed.

`internal/convention` does not test code but the repository conventions: no
standard `log` package, no inline error strings, no emoji.
