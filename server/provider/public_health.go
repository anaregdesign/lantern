package provider

import (
	"connectrpc.com/grpchealth"
	"context"
	"github.com/anaregdesign/lantern/server/internal/readinessdiag"
)

type publicHealthChecker struct {
	inner   *grpchealth.StaticChecker
	runtime *SecurityRuntime
}

func (h publicHealthChecker) Check(ctx context.Context, req *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	response, err := h.inner.Check(ctx, req)
	var observation *readinessdiag.Observer
	if h.runtime != nil {
		observation = h.runtime.readinessObservation
	}
	if err != nil {
		observation.Health("error", "not_evaluated")
	} else if response.Status != grpchealth.StatusServing {
		observation.Health("static_rejection", "not_evaluated")
	} else {
		ready, reason := h.runtime.readyObserved(ctx)
		if !ready {
			observation.Health("runtime_rejection", reason)
			return &grpchealth.CheckResponse{Status: grpchealth.StatusNotServing}, nil
		}
		observation.Health("serving", reason)
	}
	return response, err
}
