package provider

import (
	"connectrpc.com/grpchealth"
	"context"
)

type publicHealthChecker struct {
	inner   *grpchealth.StaticChecker
	runtime *SecurityRuntime
}

func (h publicHealthChecker) Check(ctx context.Context, req *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	response, err := h.inner.Check(ctx, req)
	if err == nil && response.Status == grpchealth.StatusServing && !h.runtime.Ready(ctx) {
		return &grpchealth.CheckResponse{Status: grpchealth.StatusNotServing}, nil
	}
	return response, err
}
