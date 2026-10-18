package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
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
	Reason           string             `json:"reason"`
	Node             int                `json:"node"`
	Port             uint16             `json:"port"`
	ElapsedMillis    int64              `json:"elapsed_ms"`
	Probe            string             `json:"probe"`
	HTTPStatus       int                `json:"http_status"`
	ChildExited      bool               `json:"child_exited"`
	ChildExitCode    int                `json:"child_exit_code"`
	ServerLog        string             `json:"server_log"`
	LastHealth       string             `json:"last_health"`
	LastCapability   string             `json:"last_capability"`
	HealthProbes     uint32             `json:"health_probes"`
	CapabilityProbes uint32             `json:"capability_probes"`
	Observation      fixtureObservation `json:"observation"`
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

// An adopted-listener exchange can fail before Health is available. Preserve
// the same bounded passive evidence as readiness failure without probing or
// weakening the child's certification boundary.
func fixtureAdoptionFailure(cause error, child *fixtureProcess, node, port int, elapsed time.Duration) error {
	diagnostic := fixtureReadinessDiagnostic{Reason: "request", Node: node, Port: uint16(port), ElapsedMillis: elapsed.Milliseconds(), Probe: "none", ServerLog: "unavailable", LastHealth: "not_probed", LastCapability: "not_probed", Observation: fixtureObservation{Status: "unavailable"}}
	select {
	case <-child.done:
		diagnostic.ChildExited, diagnostic.ChildExitCode = true, -1
		if child.command != nil && child.command.ProcessState != nil {
			diagnostic.ChildExitCode = child.command.ProcessState.ExitCode()
		}
	default:
	}
	if child.log != nil {
		diagnostic.ServerLog = fixtureServerLogCategory(child.log.Name())
		var observation fixtureObservationReader
		diagnostic.Observation = observation.read(child.log.Name(), time.Now())
	}
	return &fixtureFailure{stage: "spawn", cause: &fixtureReadinessError{cause: cause, diagnostic: diagnostic}}
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
		if event.Message != "server exited with error" {
			continue
		}
		category = "server_exit"
		if !strings.HasPrefix(event.Error, "initialize app: ") {
			continue
		}
		category = "initialization"
		if strings.Contains(event.Error, "open /etc/ntp.conf: no such file or directory") {
			return "source_input_missing"
		}
		if strings.Contains(event.Error, "unusable current-authority time measurement") {
			return "authority_time"
		}
		if strings.Contains(event.Error, "bind: address already in use") || strings.Contains(event.Error, "bind: Only one usage of each socket address") {
			return "bind_address_in_use"
		}
	}
	return category
}
