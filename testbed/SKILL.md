# Lantern Observability QA Harness

End-to-end QA bench that stands up the Lantern Docker image alongside a
Prometheus scraper and exercises **every public CLI and SDK surface** against
the running server while capturing logs + metrics. Used to validate releases
(this harness produced the report that became [#98][i98] / [#99][p99]).

> Skill name: **lantern-observability-qa**
> When to use: pre-release smoke, regression sweeps, reproducing
> observability bugs, evidence collection for new issues.

[i98]: https://github.com/anaregdesign/lantern/issues/98
[p99]: https://github.com/anaregdesign/lantern/pull/99

## Layout

```
testbed/
├── docker-compose.yml      # lantern + prom/prometheus
├── prometheus.yml          # 5s scrape against lantern:9090
├── scripts/
│   ├── exercise-cli.sh     # 28 CLI scenarios; per-step rc + log capture
│   ├── exercise-sdk.go     # 39 SDK scenarios; uses workspace replace
│   └── query-metrics.sh    # curl /metrics → json snapshots
└── out/                    # gitignored — logs, metric snapshots, reports
```

The `lantern` service in compose defaults to `image: ${LANTERN_IMAGE:-lantern:local}`
so you can flip between a locally-built image and a pinned release with
`LANTERN_IMAGE=ghcr.io/anaregdesign/lantern:v0.7.1 docker compose up -d`.

## End-to-end procedure

From the **repo root**:

```bash
# 0. (optional) (re)build the local image with version metadata
docker build \
  --build-arg VERSION=$(git describe --tags --always) \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  -t lantern:local .

# 1. stand up stack
cd testbed
docker compose up -d
docker compose ps     # both services healthy

# 2. exhaustive CLI sweep (writes one log per scenario under out/cli/)
bash scripts/exercise-cli.sh

# 3. exhaustive SDK sweep (writes out/sdk/report.txt + per-step lines)
go run ./testbed/scripts/exercise-sdk.go     # run from REPO ROOT, not testbed/

# 4. snapshot Prometheus + capture server logs
bash scripts/query-metrics.sh                # → out/metrics/*.json
docker compose logs lantern --no-color > out/lantern-full.log

# 5. teardown
docker compose down -v
```

## What "green" looks like

- `exercise-cli.sh`: 28 logs in `out/cli/`. Exactly three may exit non-zero
  (the deliberate `NotFound` checks: `vertex get missing`, `edge get missing`,
  `vertex delete missing`). Anything else is a regression.
- `exercise-sdk.go`: prints `ok` for all 39 steps, ending with
  `SDK report written to .../out/sdk/report.txt`.
- `lantern_build_info` reports the real release tag/commit (post-[#99][p99]).
  If it still shows `version="(devel)"`, you're running a pre-#99 build.

## Useful one-liners

Per-RPC method coverage after a sweep (proves the harness actually hit every
method — `grpc_server_*` is wired through the in-house Connect interceptor in
`server/provider/connect_middleware.go`; the names are retained for dashboard
compat after the grpc-ecosystem middleware was removed in #337/#352):

```bash
curl -s http://localhost:9090/metrics \
  | grep '^grpc_server_handled_total{' \
  | awk -F'grpc_method="' '{print $2}' | awk -F'"' '{print $1}' \
  | sort | uniq -c | sort -nr
```

GC + TTL eviction view:

```bash
curl -s http://localhost:9090/metrics \
  | grep -E '^(lantern_(vertices|edges|ttl_expirations_total|gc_duration_seconds_count)|process_resident_memory_bytes)'
```

Replication health (#187 — empty on single-instance, populated under
`testbed/docker-compose.yml` HA mode):

```bash
curl -s http://localhost:9090/metrics \
  | grep -E '^lantern_(replication_(applied|dropped)_total|replication_lag_seq|anti_entropy_(cycles|gaps_found)_total)'
```

Log triage (JSON logs by default — `LANTERN_LOG_FORMAT=json`):

```bash
docker compose logs lantern --no-color \
  | jq -r 'select(.level=="ERROR") | "\(.time) \(.method) \(.msg)"'
```

## Adding new scenarios

- **CLI**: append `run "<name>" "<cmd>"` to `scripts/exercise-cli.sh`. Each
  scenario writes `out/cli/NN-name.log` with `cmd`, stdout/stderr, and final
  `rc=…`. Use absolute paths (`$REPO`, `$TESTBED`) — `cd` inside the script
  changes the working dir for child commands.
- **SDK**: add a `step("name", func() error { … })` block in
  `scripts/exercise-sdk.go`. The helper logs `ok`/`FAIL`, appends to the
  in-memory report, and continues so a single failing scenario doesn't mask
  later ones. Value accessors (`StringValue`, `IntValue`, …) return
  `(T, error)` — always destructure.

## Reporting an issue from a sweep

1. Snapshot the offending state under `out/` (metrics JSON + relevant
   `out/cli/*.log` and/or `out/sdk/report.txt`).
2. Capture the build label so we know what we hit:
   `curl -s http://localhost:9090/metrics | grep lantern_build_info`.
3. Check readiness state — `curl -s -o /dev/null -w '%{http_code}\n'
   http://localhost:9090/healthz/ready`. 200 means the node is serving;
   503 means it is bootstrapping or behind on replication
   (`LANTERN_MAX_REPLICATION_LAG`, default 10000). `/readyz` mirrors the
   same signal.
4. File the issue with **Repro** (compose-up + the failing scenario) +
   **Observed metric/log evidence** + **Proposed fix**. The [#98][i98] body is
   a usable template.

## Why this lives in-tree

Each component (compose file, scripts, SDK harness) takes a hard dependency on
the in-repo modules (`go.work` `replace` for `exercise-sdk.go`; current
`Dockerfile`/CLI for `exercise-cli.sh`). Keeping it here means every PR can
re-run the same harness against its own build — no version skew between the
QA bench and the code being tested.
