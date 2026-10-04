package bench_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedProcessesStopsAndReapsOnlyRegisteredChildren(t *testing.T) {
	helper, err := filepath.Abs("owned_processes.sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	const script = `set -euo pipefail
source "$1"
sleep 30 & unrelated=$!
trap 'kill "$unrelated" 2>/dev/null || true; wait "$unrelated" 2>/dev/null || true' EXIT
sleep 30 & producer=$!
sleep 30 & consumer=$!
( trap '' TERM; exec sleep 30 ) & stubborn=$!
sleep 0.05
stop_owned_processes "$producer" "$consumer" "$stubborn" ""
for stopped in "$producer" "$consumer" "$stubborn"; do
  if kill -0 "$stopped" 2>/dev/null; then exit 10; fi
done
kill -0 "$unrelated"
if stop_owned_processes "not-a-pid"; then exit 11; fi
`
	if output, err := exec.CommandContext(ctx, "bash", "-c", script, "owned-process-test", helper).CombinedOutput(); err != nil {
		t.Fatalf("owned shutdown: %v\n%s", err, output)
	}
}
