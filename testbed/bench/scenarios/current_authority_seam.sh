#!/usr/bin/env bash
# Bounded S4 admission/codec seam. Synthetic qualified time is deliberate;
# this is not Server OFF/ON, HA load, native clock or release qualification.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"
go test ./server/internal/security \
  -run '^TestCurrentWarmAdmissionUsesNoPeerJournalOrTimeRefresh$' \
  -bench '^BenchmarkCurrentPublicOutputCodec$' -benchtime=100x -count=1
