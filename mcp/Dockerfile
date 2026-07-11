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
# Distroless static + nonroot. The MCP server serves Streamable HTTP; it
# needs no shell, no package manager, and no writable filesystem.
FROM gcr.io/distroless/static:nonroot

# Default Lantern endpoint and ping timeout. Override at runtime via -e.
# LANTERN_MCP_HTTP_ADDR binds 0.0.0.0 (not the loopback default the bare
# binary uses) because the endpoint must be reachable through the
# published container port. The endpoint is unauthenticated, so only
# publish it on trusted networks — the handler still applies cross-origin
# and DNS-rebinding protection (see mcp/README.md). The TTL ladder is
# loaded from LANTERN_MCP_TTL_<BUCKET>=<duration> env overrides; see
# mcp/internal/ttl for the 12 bucket names.
# Set LANTERN_MCP_AGENT_ID per replica for stable fleet identity, and
# LANTERN_TOKEN when the upstream runs with LANTERN_AUTH_TOKENS (#850).
ENV LANTERN_ADDR=http://localhost:6380 \
    LANTERN_MCP_PING_TIMEOUT=3s \
    LANTERN_MCP_HTTP_ADDR=0.0.0.0:6390

COPY --from=builder /out/lantern-mcp /lantern-mcp

# The MCP server listens for Streamable HTTP on :6390 and serves the MCP
# endpoint at /mcp plus a /healthz probe. SIGTERM triggers a graceful
# drain, so the default distroless stop signal works without quirks.
EXPOSE 6390
USER nonroot:nonroot
ENTRYPOINT ["/lantern-mcp"]
