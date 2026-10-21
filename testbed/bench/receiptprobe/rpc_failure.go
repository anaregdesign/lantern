package main

import (
	"encoding/json"
	"errors"
	"io"

	"connectrpc.com/connect"
)

// rpcFailureClassification retains only fixed diagnostic categories. A wire
// error identifies a wire-encoded status, not its originating process or
// whether the mutation committed. Message classes describe exact literals; they
// do not independently prove a server execution branch.
type rpcFailureClassification struct {
	Source string
	Class  string
}

func classifyRPCFailure(err error) rpcFailureClassification {
	if err == nil {
		return rpcFailureClassification{}
	}
	result := rpcFailureClassification{Source: "client", Class: "unclassified"}
	if !connect.IsWireError(err) {
		return result
	}
	result.Source = "wire"
	var rpcErr *connect.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code() != connect.CodeInternal {
		return result
	}
	// Match only complete, fixed messages from the existing Edge Add path.
	// Wrapped/joined messages containing any extra text remain unclassified;
	// no raw message or variable identity escapes this function.
	switch rpcErr.Message() {
	case "mutationreceipt: clock high-water persistence failed":
		result.Class = "receipt_high_water"
	case "mutationreceipt: invalid transaction state":
		result.Class = "receipt_transaction"
	case "staged Edge Add result alignment drift":
		result.Class = "result_alignment"
	case "staged Edge Add accepted index drift":
		result.Class = "accepted_index"
	case "receipt Edge Add could not stage contiguous origin seq":
		result.Class = "origin_stage"
	case "mutation origin state is unavailable":
		result.Class = "origin_state"
	case "graphcache: staged Add requires a publication gate",
		"graphcache: vertex prefix policy changed during staged Add":
		result.Class = "graph_stage"
	case "service: invalid receipt WAL union payload: invalid receipt Add origin, HLC, epoch, or policy metadata",
		"service: invalid receipt WAL union payload: invalid receipt Add request alignment or item count",
		"service: invalid receipt WAL union payload: zero receipt Add logical-call ID":
		result.Class = "envelope"
	case "internal server error":
		result.Class = "generic_internal"
	}
	return result
}

// writeRPCFailureDiagnostics writes at most the first failed admission and
// lookup of a completed phase. Diagnostic write failures cannot change the
// workload verdict. No sample reason, latency, identity, or payload is written.
func writeRPCFailureDiagnostics(w io.Writer, phase string, admission, lookup []rpcSample) {
	if w == nil {
		return
	}
	if phase != "warmup" && phase != "steady" {
		phase = "other"
	}
	encoder := json.NewEncoder(w)
	writeFirst := func(stage string, samples []rpcSample) {
		for _, sample := range samples {
			if sample.status == "OK" {
				continue
			}
			status := rpcFailureStatus(sample.status)
			source := sample.failure.Source
			if source != "wire" && source != "client" {
				source = "probe_validation"
			}
			class := "unclassified"
			if source == "wire" && status == "Internal" {
				switch sample.failure.Class {
				case "receipt_high_water", "receipt_transaction", "result_alignment", "accepted_index",
					"origin_stage", "origin_state", "graph_stage", "envelope", "generic_internal":
					class = sample.failure.Class
				}
			}
			_ = encoder.Encode(struct {
				Event  string `json:"event"`
				Phase  string `json:"phase"`
				Stage  string `json:"stage"`
				Status string `json:"status"`
				Source string `json:"source"`
				Class  string `json:"class"`
			}{"rpc_failure", phase, stage, status, source, class})
			return
		}
	}
	writeFirst("admission", admission)
	writeFirst("lookup", lookup)
}

func rpcFailureStatus(status string) string {
	switch status {
	case "Canceled", "Unknown", "InvalidArgument", "DeadlineExceeded", "NotFound", "AlreadyExists",
		"PermissionDenied", "ResourceExhausted", "FailedPrecondition", "Aborted", "OutOfRange",
		"Unimplemented", "Internal", "Unavailable", "DataLoss", "Unauthenticated":
		return status
	default:
		return "Unknown"
	}
}
