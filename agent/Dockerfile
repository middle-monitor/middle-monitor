# Agent image, for the Kubernetes DaemonSet and Docker hosts that would rather
# run the agent as a container than install the binary.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
WORKDIR /agent
ARG AGENT_VERSION=dev
ARG TARGETOS TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags "-X main.Version=${AGENT_VERSION} -s -w" -o /middle-monitor-agent .

# coreutils: the disk metrics parse GNU df output.
FROM alpine:3.19
RUN apk add --no-cache ca-certificates coreutils
COPY --from=builder /middle-monitor-agent /usr/local/bin/middle-monitor-agent
ENTRYPOINT ["/usr/local/bin/middle-monitor-agent"]
