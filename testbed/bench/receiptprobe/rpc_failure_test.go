package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestClassifyRPCFailure(t *testing.T) {
	t.Parallel()
	if got := classifyRPCFailure(nil); got != (rpcFailureClassification{}) {
		t.Fatalf("nil classification = %#v", got)
	}
	for _, tc := range []struct{ message, class string }{
		{"mutationreceipt: clock high-water persistence failed", "receipt_high_water"},
		{"mutationreceipt: invalid transaction state", "receipt_transaction"},
		{"staged Edge Add result alignment drift", "result_alignment"},
		{"staged Edge Add accepted index drift", "accepted_index"},
		{"receipt Edge Add could not stage contiguous origin seq", "origin_stage"},
		{"mutation origin state is unavailable", "origin_state"},
		{"graphcache: staged Add requires a publication gate", "graph_stage"},
		{"graphcache: vertex prefix policy changed during staged Add", "graph_stage"},
		{"service: invalid receipt WAL union payload: invalid receipt Add origin, HLC, epoch, or policy metadata", "envelope"},
		{"service: invalid receipt WAL union payload: invalid receipt Add request alignment or item count", "envelope"},
		{"service: invalid receipt WAL union payload: zero receipt Add logical-call ID", "envelope"},
		{"internal server error", "generic_internal"},
	} {
		t.Run(tc.class+"/"+tc.message, func(t *testing.T) {
			wire := connect.NewWireError(connect.CodeInternal, errors.New(tc.message))
			want := rpcFailureClassification{Source: "wire", Class: tc.class}
			if got := classifyRPCFailure(wire); got != want {
				t.Fatalf("wire classification = %#v, want %#v", got, want)
			}
			wrapped := fmt.Errorf("private wrapper token: %w", wire)
			if got := classifyRPCFailure(wrapped); got != want {
				t.Fatalf("wrapped wire classification = %#v, want %#v", got, want)
			}
			client := connect.NewError(connect.CodeInternal, errors.New(tc.message))
			if got := classifyRPCFailure(client); got != (rpcFailureClassification{"client", "unclassified"}) {
				t.Fatalf("client message classified as server branch: %#v", got)
			}
			wrongCode := connect.NewWireError(connect.CodeUnavailable, errors.New(tc.message))
			if got := classifyRPCFailure(wrongCode); got != (rpcFailureClassification{"wire", "unclassified"}) {
				t.Fatalf("non-Internal message classified as Internal branch: %#v", got)
			}
		})
	}
}

func TestClassifyRPCFailureRejectsVariableMessages(t *testing.T) {
	t.Parallel()
	const secret = "Bearer secret-token https://private.invalid/path payload=private op-id=123 digest=abcd"
	for _, message := range []string{
		"", secret,
		"staged Edge Add result alignment drift " + secret,
		secret + "staged Edge Add result alignment drift",
		"staged Edge Add result alignment drift\n",
		"receipt Edge Add item 0 has invalid original result",
		errors.Join(errors.New("mutationreceipt: clock high-water persistence failed"), errors.New(secret)).Error(),
	} {
		got := classifyRPCFailure(connect.NewWireError(connect.CodeInternal, errors.New(message)))
		if got != (rpcFailureClassification{"wire", "unclassified"}) {
			t.Fatalf("variable message escaped strict classification: %#v", got)
		}
	}
	// Non-wire errors need no message access, even for arbitrary transports.
	if got := classifyRPCFailure(rpcFailureUnreadableError{}); got != (rpcFailureClassification{"client", "unclassified"}) {
		t.Fatalf("transport classification = %#v", got)
	}
	if got := classifyRPCFailure(connect.NewWireError(connect.CodeUnavailable, rpcFailureUnreadableError{})); got != (rpcFailureClassification{"wire", "unclassified"}) {
		t.Fatalf("non-Internal classification = %#v", got)
	}
}

func TestWriteRPCFailureDiagnostics(t *testing.T) {
	t.Parallel()
	const secret = "Bearer secret-token https://private.invalid/path payload=private op-id=123 digest=abcd"
	admission := []rpcSample{
		{status: "OK", reason: secret},
		{status: "Internal", reason: secret, failure: classifyRPCFailure(connect.NewWireError(connect.CodeInternal,
			errors.New("receipt Edge Add could not stage contiguous origin seq")))},
		{status: secret, reason: secret},
	}
	lookup := []rpcSample{
		{status: "OK"},
		{status: "Internal", reason: secret, failure: classifyRPCFailure(connect.NewWireError(connect.CodeInternal, errors.New(secret)))},
		{status: "Unavailable"},
	}
	var output bytes.Buffer
	writeRPCFailureDiagnostics(&output, "steady", admission, lookup)
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("diagnostic lines = %d, want 2", len(lines))
	}
	for i, want := range []map[string]string{
		{"event": "rpc_failure", "phase": "steady", "stage": "admission", "status": "Internal", "source": "wire", "class": "origin_stage"},
		{"event": "rpc_failure", "phase": "steady", "stage": "lookup", "status": "Internal", "source": "wire", "class": "unclassified"},
	} {
		var got map[string]string
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("record %d contains unexpected fields: %#v", i, got)
		}
		for key, value := range want {
			if got[key] != value {
				t.Fatalf("record %d field %s = %q, want %q", i, key, got[key], value)
			}
		}
	}
	if strings.Contains(output.String(), secret) || strings.Contains(output.String(), "private") || strings.Contains(output.String(), "latency") {
		t.Fatal("diagnostic contains private or unapproved sample data")
	}
	if admission[1].reason != secret || lookup[1].status != "Internal" {
		t.Fatal("diagnostics modified samples")
	}

	output.Reset()
	writeRPCFailureDiagnostics(&output, "warmup", []rpcSample{{status: "OK"}}, nil)
	writeRPCFailureDiagnostics(nil, "steady", admission, lookup)
	if output.Len() != 0 {
		t.Fatal("successful phase emitted a failure diagnostic")
	}
	writer := &rpcFailureBrokenWriter{}
	writeRPCFailureDiagnostics(writer, "steady", admission, lookup)
	if writer.calls != 1 {
		t.Fatalf("failed writer calls = %d, want one attempt without retry", writer.calls)
	}
}

func TestWriteRPCFailureDiagnosticsSanitizesAllFields(t *testing.T) {
	t.Parallel()
	const secret = "secret-url-token-body-operation-id"
	for _, tc := range []struct {
		sample rpcSample
		source string
	}{
		{rpcSample{status: secret, reason: secret, failure: rpcFailureClassification{secret, secret}}, "probe_validation"},
		{rpcSample{status: "DataLoss", reason: secret, failure: rpcFailureClassification{"client", "origin_stage"}}, "client"},
		{rpcSample{status: "Unavailable", reason: secret, failure: rpcFailureClassification{"wire", "origin_stage"}}, "wire"},
		{rpcSample{status: "Internal", reason: secret, failure: rpcFailureClassification{"wire", secret}}, "wire"},
	} {
		var output bytes.Buffer
		writeRPCFailureDiagnostics(&output, secret, []rpcSample{tc.sample}, nil)
		var got map[string]string
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), secret) || got["phase"] != "other" || got["class"] != "unclassified" || got["source"] != tc.source {
			t.Fatalf("unapproved field escaped diagnostic: %#v", got)
		}
		if tc.sample.status == secret && got["status"] != "Unknown" {
			t.Fatalf("unapproved status = %q", got["status"])
		}
	}
	for _, status := range []string{"Canceled", "Unknown", "InvalidArgument", "DeadlineExceeded", "NotFound", "AlreadyExists",
		"PermissionDenied", "ResourceExhausted", "FailedPrecondition", "Aborted", "OutOfRange", "Unimplemented",
		"Internal", "Unavailable", "DataLoss", "Unauthenticated"} {
		if got := rpcFailureStatus(status); got != status {
			t.Fatalf("known status %s changed to %s", status, got)
		}
	}
}

type rpcFailureUnreadableError struct{}

func (rpcFailureUnreadableError) Error() string { panic("non-wire error message must not be read") }

type rpcFailureBrokenWriter struct{ calls int }

func (w *rpcFailureBrokenWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, errors.New("diagnostic sink unavailable")
}
