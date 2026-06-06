// Package client: health.go owns the SDK's Ping helper. After the
// #347 listener cutover the Lantern primary port mounts the
// gRPC-Health-v1 surface via connectrpc.com/grpchealth, which accepts
// the same Connect / gRPC / gRPC-Web protocols the rest of the
// Connect mux speaks. The SDK uses Connect+JSON for the Check call so
// it can reuse the same *http.Client / baseURL the Lantern client was
// constructed with — no separate metrics-port plumbing required (the
// old PingConnect helper that hit /healthz on the metrics listener is
// retired).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// grpcHealthCheckProcedure is the URL path the connectrpc.com/grpchealth
// handler exposes. Locked by the gRPC-Health-v1 spec — never override.
const grpcHealthCheckProcedure = "/grpc.health.v1.Health/Check"

// healthCheckRequest is the Connect+JSON wire shape of
// grpc.health.v1.HealthCheckRequest. The "service" field defaults to
// the overall process status, which on Lantern is wired to the
// readiness Gate (#188). Passing a non-empty service name is supported
// but rare in practice.
type healthCheckRequest struct {
	Service string `json:"service,omitempty"`
}

// healthCheckResponse is the Connect+JSON wire shape of
// grpc.health.v1.HealthCheckResponse. The proto enum field renders as
// its symbolic name per the proto3 JSON spec. The
// connectrpc.com/grpchealth handler — which Lantern uses to mount the
// gRPC-Health-v1 surface — bundles its own proto descriptor whose enum
// constants are prefixed: SERVING_STATUS_SERVING /
// SERVING_STATUS_NOT_SERVING / SERVING_STATUS_UNSPECIFIED /
// SERVING_STATUS_SERVICE_UNKNOWN. The legacy
// google.golang.org/grpc/health proto uses the shorter SERVING /
// NOT_SERVING / UNKNOWN / SERVICE_UNKNOWN. We accept either shape via
// servingStatusOK.
type healthCheckResponse struct {
	Status string `json:"status"`
}

// servingStatusOK reports whether the JSON status string maps to a
// SERVING reply, regardless of which gRPC-Health-v1 proto descriptor
// the server happens to be using.
func servingStatusOK(status string) bool {
	switch status {
	case "SERVING", "SERVING_STATUS_SERVING":
		return true
	default:
		return false
	}
}

// Ping issues a Health/Check against the Lantern primary listener and
// returns nil iff the server reports SERVING. Useful as a readiness
// probe or a one-liner connectivity test. Pass a context with a
// deadline to bound the call.
//
// Implementation: Connect+JSON POST to /grpc.health.v1.Health/Check
// over the same http.Client + baseURL the Lantern client was built
// with. No additional plumbing — the gRPC-Health-v1 surface is mounted
// on the primary listener via connectrpc.com/grpchealth since #347.
func (l *Lantern) Ping(ctx context.Context) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	if l.connectHTTPClient == nil || l.connectBaseURL == "" {
		return errors.New("client: Ping requires a Lantern constructed via NewLantern / NewLanternConnect")
	}
	body, err := json.Marshal(healthCheckRequest{})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		l.connectBaseURL+grpcHealthCheckProcedure,
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := l.connectHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Drain so the connection can be reused even on error.
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("client: Ping returned HTTP %d", resp.StatusCode)
	}
	var hcr healthCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&hcr); err != nil {
		return fmt.Errorf("client: Ping decode: %w", err)
	}
	if !servingStatusOK(hcr.Status) {
		return &healthStatusError{status: hcr.Status}
	}
	return nil
}

// healthStatusError surfaces a non-SERVING reply so callers can branch
// via errors.As. The legacy gRPC-health-v1 enum was an integer; the
// Connect+JSON wire emits the symbolic name, which is more useful for
// log messages anyway.
type healthStatusError struct {
	status string
}

func (e *healthStatusError) Error() string {
	return "lantern: server health status = " + e.status
}
