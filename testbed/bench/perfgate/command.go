package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "snapshot":
		code = runSnapshot(os.Args[2:])
	case "evaluate":
		code = runEvaluate(os.Args[2:])
	default:
		usage()
		code = 2
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: perfgate snapshot -endpoints URL,... -out FILE | perfgate evaluate -scenario FILE -run-dir DIR -out FILE [-lifecycle-pre FILE -lifecycle-post FILE] [-producer-failed]")
}

func runSnapshot(args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	endpoints := fs.String("endpoints", "", "comma-separated Prometheus /metrics URLs")
	out := fs.String("out", "", "output JSON path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *endpoints == "" || *out == "" {
		fs.Usage()
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snapshot, err := captureSearchCounters(ctx, http.DefaultClient, splitNonEmpty(*endpoints))
	if err != nil {
		fmt.Fprintf(os.Stderr, "perfgate snapshot: %v\n", err)
		return 1
	}
	if err := writeJSON(*out, snapshot); err != nil {
		fmt.Fprintf(os.Stderr, "perfgate snapshot: %v\n", err)
		return 1
	}
	return 0
}

func runEvaluate(args []string) int {
	fs := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	scenario := fs.String("scenario", "", "resolved scenario YAML")
	runDir := fs.String("run-dir", "", "scenario artifact directory")
	pre := fs.String("lifecycle-pre", "", "pre-steady Search counter snapshot")
	post := fs.String("lifecycle-post", "", "post-steady Search counter snapshot")
	producerFailed := fs.Bool("producer-failed", false, "one or more steady ghz processes exited non-zero")
	out := fs.String("out", "", "perf_gate.json path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scenario == "" || *runDir == "" || *out == "" {
		fs.Usage()
		return 2
	}
	report, err := evaluate(*scenario, *runDir, *pre, *post)
	if err != nil {
		report = perfGateReport{Verdict: "fail", Failures: []string{err.Error()}}
	}
	if *producerFailed {
		report.Failures = append(report.Failures, "one or more steady ghz producer processes exited non-zero")
		report.Verdict = "fail"
	}
	if writeErr := writeJSON(*out, report); writeErr != nil {
		fmt.Fprintf(os.Stderr, "perfgate evaluate: %v\n", writeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "perfgate evaluate: %v\n", err)
	}
	if report.Verdict != "pass" {
		return 1
	}
	return 0
}
