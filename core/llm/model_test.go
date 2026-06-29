package llm

import (
	"context"
	"errors"
	"testing"
)

func TestModelFunc_Generate(t *testing.T) {
	wantErr := errors.New("boom")
	var gotInput string
	f := ModelFunc[person](func(_ context.Context, input string) (Response[person], error) {
		gotInput = input
		return Response[person]{
			Output:       person{Name: "Ada", Age: 36},
			Usage:        Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			FinishReason: FinishStop,
			Model:        "fake-1",
		}, wantErr
	})

	// ModelFunc[T] must satisfy Model[T].
	var m Model[person] = f

	resp, err := m.Generate(context.Background(), "x")

	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if resp.Output != (person{Name: "Ada", Age: 36}) {
		t.Errorf("Output = %+v", resp.Output)
	}
	if resp.Usage != (Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}) {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.FinishReason != FinishStop {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, FinishStop)
	}
	if resp.Model != "fake-1" {
		t.Errorf("Model = %q, want fake-1", resp.Model)
	}
	if gotInput != "x" {
		t.Errorf("forwarded input = %q, want x", gotInput)
	}
}
