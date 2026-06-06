# syntax=docker/dockerfile:1.7

# --- build stage ---------------------------------------------------------
# Multi-stage build for the lantern-mcp server. The build context MUST be
# the repo root (not mcp/) because mcp/go.mod has replace directives that
# point at sibling modules (../pb, ../sdks/go) and go.work pins all six
# workspace members.
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache module downloads independently of source changes. Every workspace
# member's go.mod + go.sum must be present BEFORE `go mod download` because
# go.work loads them eagerly.
COPY go.mod go.sum go.work go.work.sum* ./
COPY core/go.mod core/go.sum ./core/
COPY mcp/go.mod mcp/go.sum ./mcp/
COPY pb/go.mod pb/go.sum ./pb/
COPY sdks/go/go.mod sdks/go/go.sum ./sdks/go/
COPY server/go.mod server/go.sum ./server/
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
# VERSION is stamped into `mcp.Version` via -ldflags so the MCP server can
# self-report its release in handshake responses. CI sets it from the tag.
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd mcp && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath \
      -ldflags="-s -w -X github.com/anaregdesign/lantern/mcp.Version=${VERSION}" \
      -o /out/lantern-mcp ./cmd

# --- final stage ---------------------------------------------------------
# Distroless static + nonroot. The MCP server speaks stdio only; it needs
# no shell, no package manager, and no writable filesystem.
FROM gcr.io/distroless/static:nonroot

# Default Lantern endpoint and ping timeout. Override at runtime via -e.
# The TTL ladder is loaded from LANTERN_MCP_TTL_<BUCKET>=<duration> env
# overrides; see mcp/internal/ttl for the 12 bucket names.
ENV LANTERN_ADDR=http://localhost:6380 \
    LANTERN_MCP_PING_TIMEOUT=3s

COPY --from=builder /out/lantern-mcp /lantern-mcp

# The MCP transport owns stdin/stdout; MCP clients invoke the binary
# directly and tear it down when the session ends. There are no ports
# to expose and no signal-handling quirks (the SDK reads to EOF).
USER nonroot:nonroot
ENTRYPOINT ["/lantern-mcp"]
