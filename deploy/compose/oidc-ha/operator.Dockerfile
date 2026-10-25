# syntax=docker/dockerfile:1.7
# Build this external operator from the SAME checkout as the serving image.
FROM golang:1.27.3-alpine AS builder
WORKDIR /src
COPY go.mod go.sum go.work go.work.sum* ./
COPY core/go.mod core/go.sum ./core/
COPY mcp/go.mod mcp/go.sum ./mcp/
COPY pb/go.mod pb/go.sum ./pb/
COPY sdks/go/go.mod sdks/go/go.sum ./sdks/go/
COPY server/go.mod server/go.sum ./server/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/membershippublisher ./server/cmd/membershippublisher
FROM alpine:3.24
COPY --from=builder /out/membershippublisher /membershippublisher
USER 65532:65532
STOPSIGNAL SIGTERM
ENTRYPOINT ["/membershippublisher"]
