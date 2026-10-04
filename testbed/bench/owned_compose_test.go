package bench_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedComposeRejectsResidueAndUnverifiableOwnership(t *testing.T) {
	helper, err := filepath.Abs("owned_compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"none", "down", "container", "volume", "network", "inspect", "different_project"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			const script = `set -euo pipefail
source "$1"
export COMPOSE_PROJECT_NAME=lantern-bench-owned-fixture
failure="$2"
docker() {
  case "$*" in
    "compose -f fixture.yml --profile off-diagnostics down -v --remove-orphans") [[ "$failure" != down ]];;
    "ps -aq --filter label=com.docker.compose.project=lantern-bench-owned-fixture")
      [[ "$failure" != inspect ]] || return 19
      if [[ "$failure" == container ]]; then echo residue; fi;;
    "volume ls -q --filter label=com.docker.compose.project=lantern-bench-owned-fixture")
      if [[ "$failure" == volume ]]; then echo residue; fi;;
    "network ls -q --filter label=com.docker.compose.project=lantern-bench-owned-fixture")
      if [[ "$failure" == network ]]; then echo residue; fi;;
    *) return 89;;
  esac
}
project="$COMPOSE_PROJECT_NAME"
if [[ "$failure" == different_project ]]; then project=unrelated; fi
if down_owned_compose "$project" -f fixture.yml; then
  [[ "$failure" == none ]]
else
  [[ "$failure" != none ]]
fi
`
			if output, err := exec.CommandContext(ctx, "bash", "-c", script, "owned-compose-test", helper, failure).CombinedOutput(); err != nil {
				t.Fatalf("owned Compose verification: %v\n%s", err, output)
			}
		})
	}
}
