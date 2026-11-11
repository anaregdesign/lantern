# syntax=docker/dockerfile:1.7
# Build from the repository root. This helper has standard-library dependencies only.
FROM golang:1.27.4-alpine AS builder
WORKDIR /src
COPY deploy/compose/oidc-ha/metrics-relay.go ./
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/metrics-relay metrics-relay.go

FROM scratch
ARG COMMIT=
LABEL org.opencontainers.image.revision=${COMMIT}
COPY --from=builder /out/metrics-relay /metrics-relay
USER 65532:65532
EXPOSE 9443
ENTRYPOINT ["/metrics-relay"]
