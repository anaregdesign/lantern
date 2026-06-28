package llm

import (
	"context"
	"errors"
	"testing"
)

func TestGenerate(t *testing.T) {
	t.Run("builds the request and returns a typed response", func(t *testing.T) {
		var gotReq Request[person]
		m := ModelFunc[person](func(_ context.Context, req Request[person]) (Response[person], error) {
			gotReq = req
			return Response[person]{Output: person{Name: "Ada", Age: 36}}, nil
		})

		resp, err := Generate[person](context.Background(), m, "extract", "Ada is 36")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotReq.Instruction != "extract" || gotReq.Input != "Ada is 36" {
			t.Errorf("forwarded request = %+v", gotReq)
		}
		if resp.Output != (person{Name: "Ada", Age: 36}) {
			t.Errorf("Output = %+v", resp.Output)
		}
	})

	t.Run("propagates the model error", func(t *testing.T) {
		wantErr := errors.New("backend down")
		m := ModelFunc[person](func(context.Context, Request[person]) (Response[person], error) {
			return Response[person]{}, wantErr
		})
		_, err := Generate[person](context.Background(), m, "i", "x")
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
	})
}
