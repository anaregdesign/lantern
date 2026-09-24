package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPeriodicBackupReportShowsNamedMatchedWindows(t *testing.T) {
	dir := t.TempDir()
	data := `{"sha":"abc123","vertices":100000,"seeded_edges":3200000,"backup_interval_ms":30000,"offered_read_rps_per_producer":200,"offered_write_rps":50,"periodic_ticks":[{"source":"periodic","tick_id":"42","started_unix_ns":1000000000,"finished_unix_ns":2000000000,"materialization_ns":300000000,"send_ns":600000000,"finalization_ns":100000000,"vertices":100000,"edges":3200000}],"correlations":[{"tick_id":"42","producer":"GetVertex","before":{"count":100,"errors":0,"p99_ns":1000000,"complete":true},"during":{"count":120,"errors":0,"p99_ns":3000000,"complete":true},"after":{"count":110,"errors":0,"p99_ns":2000000,"complete":true}}],"synthetic_backup_signal":"fired"}`
	if err := os.WriteFile(filepath.Join(dir, "backup_periodic.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := LoadInput(dir, "periodic_backup", "t")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RenderReport(&buf, in); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Periodic backup/read correlation", "Result: `fired`", "`42`", "300.00 | 600.00 | 100.00", "`GetVertex` | 1.00 / 100 / 0 | 3.00 / 120 / 0 | 2.00 / 110 / 0 | true"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q:\n%s", want, buf.String())
		}
	}
}
