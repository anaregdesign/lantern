# syntax=docker/dockerfile:1.7

# --- build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache module downloads independently of source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Static binary; suitable for distroless/scratch and for alpine.
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/lantern ./server/cmd

# --- final stage ---------------------------------------------------------
FROM alpine:3.20

RUN addgroup -S lantern && adduser -S -G lantern lantern

ENV LANTERN_DEFAULT_TTL_SECONDS=3600 \
    LANTERN_PORT=6380

WORKDIR /app
COPY --from=builder /out/lantern /app/lantern

USER lantern
EXPOSE 6380

ENTRYPOINT ["/app/lantern"]
