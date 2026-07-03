#!/usr/bin/env bash
# Regenerates the pinned Lucene baseline (core/search/relevance/testdata/lucene_runs.json)
# from the golden corpora. Run it whenever a corpus fixture changes, then commit
# the refreshed runs file together with the fixture edit. Procedure + rationale:
# testbed/SKILL.md "Lucene relevance baseline".
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
testdata="$(cd "$here/../../core/search/relevance/testdata" && pwd)"

docker build -t lantern-lucene-baseline "$here"
docker run --rm \
  -v "$testdata:/data" \
  lantern-lucene-baseline /data /data/lucene_runs.json

echo "pinned: $testdata/lucene_runs.json"
