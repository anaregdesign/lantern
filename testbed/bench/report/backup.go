package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BackupStressFile is the content-free periodic Backupper and named h2c
// producer correlation artifact. Call-level samples remain in the JSON; the
// report shows its matched per-tick windows for review.
type BackupStressFile struct {
	SHA         string `json:"sha"`
	Vertices    int    `json:"vertices"`
	SeededEdges int    `json:"seeded_edges"`
	IntervalMS  int    `json:"backup_interval_ms"`
	ReadRPS     int    `json:"offered_read_rps_per_producer"`
	WriteRPS    int    `json:"offered_write_rps"`
	Attempts    []struct {
		TickID    string `json:"tick_id"`
		Completed bool   `json:"completed"`
	} `json:"periodic_attempts"`
	Ticks []struct {
		Source            string `json:"source"`
		TickID            string `json:"tick_id"`
		StartNS           int64  `json:"started_unix_ns"`
		EndNS             int64  `json:"finished_unix_ns"`
		MaterializationNS int64  `json:"materialization_ns"`
		SendNS            int64  `json:"send_ns"`
		FinalizationNS    int64  `json:"finalization_ns"`
		Vertices          int    `json:"vertices"`
		Edges             int    `json:"edges"`
	} `json:"periodic_ticks"`
	Correlations []struct {
		TickID   string             `json:"tick_id"`
		Producer string             `json:"producer"`
		Before   BackupStressWindow `json:"before"`
		During   BackupStressWindow `json:"during"`
		After    BackupStressWindow `json:"after"`
	} `json:"correlations"`
	Signal string `json:"synthetic_backup_signal"`
}

type BackupStressWindow struct {
	Count     int     `json:"count"`
	Errors    int     `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
	P99NS     int64   `json:"p99_ns"`
	Complete  bool    `json:"complete"`
}

func loadBackupStress(dir string, in *Input) error {
	const name = "backup_periodic.json"
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file BackupStressFile
	if err := json.Unmarshal(b, &file); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	in.Backup = &file
	return nil
}

func renderBackupStress(w *errWriter, file *BackupStressFile) {
	w.printf("## Periodic backup/read correlation\n\n")
	if file == nil {
		w.printf("_no periodic backup artifact found_\n\n")
		return
	}
	w.printf("Synthetic host run at SHA `%s`: %d vertices / %d edges, %d ms backup interval, %d offered calls/s per named read and %d writes/s; %d/%d periodic attempts completed. Result: `%s`. A window needs 100 successful calls and no errors; otherwise its p99 is shown for context but the comparison is inconclusive. A failed attempt breaks a three-tick streak.\n\n",
		file.SHA, file.Vertices, file.SeededEdges, file.IntervalMS, file.ReadRPS, file.WriteRPS, len(file.Ticks), len(file.Attempts), file.Signal)
	w.printf("| periodic tick | duration ms | materialization ms | send ms | finalization ms | vertices / edges |\n")
	w.printf("| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, tick := range file.Ticks {
		w.printf("| `%s` | %.2f | %.2f | %.2f | %.2f | %d / %d |\n",
			tick.TickID, nsToMs(tick.EndNS-tick.StartNS), nsToMs(tick.MaterializationNS),
			nsToMs(tick.SendNS), nsToMs(tick.FinalizationNS), tick.Vertices, tick.Edges)
	}
	w.printf("\n| periodic tick | producer | before p99 ms / count / error rate | during p99 ms / count / error rate | after p99 ms / count / error rate | comparable |\n")
	w.printf("| --- | --- | ---: | ---: | ---: | --- |\n")
	for _, row := range file.Correlations {
		comparable := row.Before.Complete && row.During.Complete && row.After.Complete
		w.printf("| `%s` | `%s` | %.2f / %d / %.3f | %.2f / %d / %.3f | %.2f / %d / %.3f | %t |\n",
			row.TickID, row.Producer,
			nsToMs(row.Before.P99NS), row.Before.Count, row.Before.ErrorRate,
			nsToMs(row.During.P99NS), row.During.Count, row.During.ErrorRate,
			nsToMs(row.After.P99NS), row.After.Count, row.After.ErrorRate, comparable)
	}
	w.printf("\n")
}
