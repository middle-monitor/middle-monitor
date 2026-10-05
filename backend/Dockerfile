# Build from the repository root (context: .)
# Stage 1: Build agent binaries
FROM golang:1.25-alpine AS agent-builder
WORKDIR /agent

# The version stamped into the binaries and published by GET /api/v1/agents/latest.
# Pass the deploy tag: `docker compose build --build-arg AGENT_VERSION=$TAG`.
# Without it the fleet is told the version is "dev", and a pinned download URL
# becomes /agents/download/dev/..., which defeats pinning entirely.
ARG AGENT_VERSION=dev

COPY agent/go.mod agent/go.sum ./
RUN go mod download
COPY agent/ .
RUN mkdir -p dist && \
    LDFLAGS="-s -w -X main.Version=${AGENT_VERSION}" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o dist/middle-monitor-agent-linux-amd64 . && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$LDFLAGS" -o dist/middle-monitor-agent-linux-arm64 . && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o dist/middle-monitor-agent-darwin-amd64 . && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o dist/middle-monitor-agent-darwin-arm64 . && \
    printf '%s\n' "${AGENT_VERSION}" > dist/VERSION

# Stage 2: Build backend binaries
FROM golang:1.25-alpine AS backend-builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -o api ./cmd/api
RUN CGO_ENABLED=0 go build -o receiver ./cmd/receiver
RUN CGO_ENABLED=0 go build -o worker ./cmd/worker

# Stage 3: Build payments service
FROM golang:1.25-alpine AS payments-builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -o payments ./cmd/payments

# Stage 4: Final payments image
FROM alpine:3.19 AS payments
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=payments-builder /app/payments .
EXPOSE 8081
CMD ["./payments"]

# Stage 5: Final api image
FROM alpine:3.19 AS api
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=backend-builder /app/api .
COPY --from=agent-builder /agent/dist ./agent/dist/
# The agent config JSON Schema is owned by the agent repository and served from
# disk at /api/v1/schemas/agent-config.json. Without it in the image, a URL the
# documentation points at answers 404.
COPY --from=agent-builder /agent/schemas ./agent/schemas/
EXPOSE 8080
CMD ["./api"]

# Stage 6: Final receiver image
FROM alpine:3.19 AS receiver
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=backend-builder /app/receiver .
COPY --from=agent-builder /agent/dist ./agent/dist/
# The agent config JSON Schema is owned by the agent repository and served from
# disk at /api/v1/schemas/agent-config.json. Without it in the image, a URL the
# documentation points at answers 404.
COPY --from=agent-builder /agent/schemas ./agent/schemas/
EXPOSE 8081
CMD ["./receiver"]

# Stage 7: Final worker image
FROM alpine:3.19 AS worker
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=backend-builder /app/worker .
CMD ["./worker"]
