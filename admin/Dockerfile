# syntax=docker/dockerfile:1.7

# --- build stage ---------------------------------------------------------
# Multi-stage build for lantern-admin. The build context MUST be the repo
# root (not admin/) because admin's `lantern-sdk` dependency points at
# the sibling `sdks/node/` module via the `file:` protocol (#409).
FROM oven/bun:1.3.14-alpine AS builder

WORKDIR /src

# Bring in the SDK source and build it inside the image so admin's
# `file:../sdks/node` dependency resolves against a pre-built dist/.
# The SDK's `bun install` resolves its own peer deps (connectrpc
# v2.x, bufbuild/protobuf v2.x), then `bun run build` emits the dual
# dist/{index,web}.{js,cjs} bundles the `lantern-sdk` and
# `lantern-sdk/web` subpath exports point at.
COPY sdks/node/package.json sdks/node/bun.lock ./sdks/node/
RUN --mount=type=cache,target=/root/.bun/install/cache \
    cd sdks/node && bun install --frozen-lockfile
COPY sdks/node ./sdks/node
RUN --mount=type=cache,target=/root/.bun/install/cache \
    cd sdks/node && bun run build

# Copy bun manifest + lockfile first so dependency installs are cached
# independently of source changes. `--frozen-lockfile` is the lockfile-
# verifying mode in bun >= 1.3 (no `bun ci` exists).
COPY admin/package.json admin/bun.lock ./admin/
RUN --mount=type=cache,target=/root/.bun/install/cache \
    cd admin && bun install --frozen-lockfile

# Everything the build actually needs from admin/. .dockerignore keeps
# node_modules, tests, build artefacts, and editor noise out of the
# context, so a plain `COPY admin ./admin` is both cheap and correct.
COPY admin ./admin

# Stamp the build with the release version so the deployed asset bundle
# can be traced back to a tag. We expose it as a Vite env var; if the
# SPA ever wants to render the version, it can read import.meta.env.VITE_VERSION.
ARG VERSION=dev
ENV VITE_VERSION=${VERSION}

RUN --mount=type=cache,target=/root/.bun/install/cache \
    cd admin && bun run build

# --- final stage ---------------------------------------------------------
# Caddy 2 Alpine — small, has zero shell-out attack surface (no package
# manager calls at runtime), and supports SPA fallback declaratively via
# `try_files`. The official caddy:*-alpine image already runs as a
# non-root `caddy` user when bound to port >= 1024, which is why the
# Caddyfile listens on :8080.
FROM caddy:2.10-alpine

# Copy in the SPA build and the Caddy config.
COPY --from=builder /src/admin/build/client /srv
COPY admin/Caddyfile /etc/caddy/Caddyfile

# The image binds :8080 (see Caddyfile). Containers running orchestrated
# can map that to whatever external port the platform exposes.
EXPOSE 8080

# Caddy's default entrypoint loads /etc/caddy/Caddyfile. We pass --adapter
# caddyfile explicitly so the format is unambiguous even if a future
# base-image change ever swaps the default.
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
