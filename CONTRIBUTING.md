# Contributing

Bug reports and pull requests are welcome. For anything larger than a fix,
open an issue first so we can agree on the approach before you spend time on it.

## Setup

- Go 1.25+, Node.js 22+, Docker
- `deploy/self-hosted` runs the whole stack. To work on one service, run
  PostgreSQL, OpenSearch and Kafka locally and start that service with `go run`
  (see `backend/README.md`).

```bash
cd backend && go test ./...          # unit tests
cd backend && make test-integration  # starts a throwaway PostgreSQL in Docker
cd frontend && npm ci && npm run dev # UI on :3000, proxies /api to :8080
cd agent && go test ./...
```

## Conventions

- Go: errors are sentinels or typed errors declared in the package `errors.go`;
  `fmt.Errorf` only wraps one of them with `%w`. Logging goes through `log/slog`
  with key/value pairs.
- Comments explain why, not what. Keep them to a line when you can.
- Backend messages are English; the API localizes what users see.
- Tests should fail when the behavior they protect changes. Name them after
  that behavior.

## Pull requests

- One change per pull request, with a description of what and why.
- CI must pass: `go vet`, Go tests (with the integration suite), ESLint,
  Vitest, the production build and the Playwright suite.
- By contributing you agree that your contribution is licensed under the
  repository license.
