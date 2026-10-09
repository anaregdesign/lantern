package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Only fixed categories cross a fixture's public startup boundary.
type fixtureFailure struct {
	stage string
	cause error
}

func (f *fixtureFailure) Error() string { return "fixture failed: " + fixtureFailureCategory(f) }
func (f *fixtureFailure) Unwrap() error { return f.cause }

func fixtureFailureCategory(err error) string {
	var failure *fixtureFailure
	if errors.As(err, &failure) {
		switch failure.stage {
		case "configuration", "generation", "spawn", "readiness", "publication", "exit", "private_input":
			return failure.stage
		}
	}
	return "configuration"
}

// These fields contain only local numbers and fixed categories. Never attach
// error strings, paths, response bodies, credentials or raw child logs here.
type fixtureReadinessDiagnostic struct {
	Reason        string `json:"reason"`
	Node          int    `json:"node"`
	Port          uint16 `json:"port"`
	ElapsedMillis int64  `json:"elapsed_ms"`
	Probe         string `json:"probe"`
	HTTPStatus    int    `json:"http_status"`
	ChildExited   bool   `json:"child_exited"`
	ChildExitCode int    `json:"child_exit_code"`
	ServerLog     string `json:"server_log"`
}

type fixtureReadinessError struct {
	cause      error
	diagnostic fixtureReadinessDiagnostic
}

func (e *fixtureReadinessError) Error() string { return e.cause.Error() }
func (e *fixtureReadinessError) Unwrap() error { return e.cause }

func writeFixtureFailure(w io.Writer, err error) {
	_, _ = fmt.Fprintln(w, "authfixture_failure:"+fixtureFailureCategory(err))
	var failure *fixtureReadinessError
	if errors.As(err, &failure) {
		raw, marshalErr := json.Marshal(failure.diagnostic)
		if marshalErr == nil {
			_, _ = fmt.Fprintf(w, "authfixture_readiness:%s\n", raw)
		}
	}
}

// Classify a bounded private log while it still exists. Only known structured
// server events are inspected; no arbitrary child text crosses this boundary.
func fixtureServerLogCategory(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "unavailable"
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(file, 64<<10))
	category := "none"
	for scanner.Scan() {
		var event struct {
			Message string `json:"msg"`
			Error   string `json:"err"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Message {
		case "failed to initialize app":
			category = "initialization"
			if strings.Contains(event.Error, "bind: address already in use") || strings.Contains(event.Error, "bind: Only one usage of each socket address") {
				return "bind_address_in_use"
			}
		case "server exited with error":
			category = "server_exit"
		}
	}
	return category
}
