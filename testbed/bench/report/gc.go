package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GCFile is a direct GraphCache.Watch stress artifact. Unlike ghz latency,
// its samples measure the raw GC tick itself, including the vertex and edge
// sweeps. The renderer recomputes quantiles from samples rather than trusting
// the artifact's precomputed summary.
type GCFile struct {
	Name         string     `json:"-"`
	SHA          string     `json:"sha"`
	Vertices     int        `json:"vertices"`
	SeededEdges  int        `json:"seeded_edges"`
	Shape        string     `json:"shape"`
	BudgetTails  int        `json:"budget_tails"`
	IntervalMS   int        `json:"interval_ms"`
	Samples      []GCSample `json:"samples"`
	ReclaimLagNS int64      `json:"max_probe_reclaim_lag_ns"`
	Unreclaimed  int        `json:"unreclaimed_probes"`
}

type GCSample struct {
	DurationNS int64 `json:"duration_ns"`
	Sweep      struct {
		ScannedTails                        int `json:"ScannedTails"`
		ScannedEdges                        int `json:"ScannedEdges"`
		ExpiredContributions                int `json:"ExpiredContributions"`
		CompactedContributionsInLiveBuckets int `json:"CompactedContributionsInLiveBuckets"`
		ZeroRemoved                         int `json:"ZeroRemoved"`
		DanglingRemoved                     int `json:"DanglingRemoved"`
		BacklogTails                        int `json:"BacklogTails"`
	} `json:"sweep"`
}

func loadGCFiles(dir string, entries []fs.DirEntry, in *Input) error {
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "gc_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		var file GCFile
		if err := json.Unmarshal(b, &file); err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		if len(file.Samples) == 0 {
			return fmt.Errorf("parse %s: no direct GC tick samples", name)
		}
		for _, sample := range file.Samples {
			if sample.DurationNS < 0 {
				return fmt.Errorf("parse %s: negative GC tick duration", name)
			}
		}
		file.Name = name
		in.GCFiles = append(in.GCFiles, file)
	}
	sort.Slice(in.GCFiles, func(i, j int) bool { return in.GCFiles[i].Name < in.GCFiles[j].Name })
	return nil
}

func renderGCTicks(w *errWriter, files []GCFile) {
	w.printf("## GC ticks\n\n")
	if len(files) == 0 {
		w.printf("_no direct GC tick artifacts found_\n\n")
		return
	}
	w.printf("`gc_p99` is nearest-rank p99 of direct raw `GraphCache.Watch` tick durations, recomputed from each file's samples. It is not RPC latency or a histogram bucket estimate. Each row is one repetition; compare only matching scale, shape, interval, and host.\n\n")
	w.printf("| file | SHA | shape | budget tails | interval ms | vertices / edges | ticks | gc_p95 ms | gc_p99 ms | max ms | scanned edges | expired contributions | compacted in live buckets | zero / dangling removed | max backlog tails | max reclaim lag ms | unreclaimed probes |\n")
	w.printf("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	type groupKey struct {
		sha, shape                string
		budget, interval, v, edge int
	}
	type run struct {
		p99   int64
		ticks int
	}
	groups := make(map[groupKey][]run)
	for _, file := range files {
		durations := make([]int64, len(file.Samples))
		var scannedEdges, expiredContributions, compactedLive, zeroRemoved, danglingRemoved int64
		maxBacklog := 0
		for i, sample := range file.Samples {
			durations[i] = sample.DurationNS
			scannedEdges += int64(sample.Sweep.ScannedEdges)
			expiredContributions += int64(sample.Sweep.ExpiredContributions)
			compactedLive += int64(sample.Sweep.CompactedContributionsInLiveBuckets)
			zeroRemoved += int64(sample.Sweep.ZeroRemoved)
			danglingRemoved += int64(sample.Sweep.DanglingRemoved)
			maxBacklog = max(maxBacklog, sample.Sweep.BacklogTails)
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		p99 := rawGCPercentile(durations, 99)
		w.printf("| `%s` | `%s` | `%s` | %d | %d | %d / %d | %d | %.2f | %.2f | %.2f | %d | %d | %d | %d / %d | %d | %.2f | %d |\n",
			file.Name, file.SHA, file.Shape, file.BudgetTails, file.IntervalMS, file.Vertices, file.SeededEdges, len(durations),
			nsToMs(rawGCPercentile(durations, 95)), nsToMs(p99), nsToMs(durations[len(durations)-1]),
			scannedEdges, expiredContributions, compactedLive, zeroRemoved, danglingRemoved, maxBacklog, nsToMs(file.ReclaimLagNS), file.Unreclaimed)
		key := groupKey{file.SHA, file.Shape, file.BudgetTails, file.IntervalMS, file.Vertices, file.SeededEdges}
		groups[key] = append(groups[key], run{p99: p99, ticks: len(durations)})
	}
	w.printf("\n**Synthetic GC trigger:** direct `gc_p99 >= 200 ms` in at least two of three comparable runs, each with at least 100 ticks. A missing or undersampled run is `unknown`.\n\n")
	w.printf("| shape | budget tails | interval ms | vertices / edges | runs | p99 >= 200 ms | verdict |\n")
	w.printf("| --- | ---: | ---: | ---: | ---: | ---: | --- |\n")
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].shape != keys[j].shape {
			return keys[i].shape < keys[j].shape
		}
		if keys[i].budget != keys[j].budget {
			return keys[i].budget < keys[j].budget
		}
		return keys[i].sha < keys[j].sha
	})
	for _, key := range keys {
		runs := groups[key]
		fired, complete := 0, len(runs) == 3
		for _, run := range runs {
			if run.ticks < 100 {
				complete = false
			}
			if run.p99 >= 200_000_000 {
				fired++
			}
		}
		verdict := "unknown"
		if complete {
			verdict = "not_fired"
			if fired >= 2 {
				verdict = "fired"
			}
		}
		w.printf("| `%s` | %d | %d | %d / %d | %d | %d | `%s` |\n",
			key.shape, key.budget, key.interval, key.v, key.edge, len(runs), fired, verdict)
	}
	w.printf("\n")
}

// rawGCPercentile uses the fixed nearest-rank definition (ceil(p*n/100)).
// sorted must be nonempty.
func rawGCPercentile(sorted []int64, p int) int64 {
	index := (p*len(sorted) + 99) / 100
	return sorted[index-1]
}
