package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// newDegreeFakeService seeds a fake backend whose per-direction degrees under
// prefix "n:" are all distinct, mirroring the core degree fixture, so handler
// ordering assertions are unambiguous.
//
//	OUT: n:a 3/7, n:b 1/1     IN: n:c 3/11, n:a 1/1, n:b 1/1
//	BOTH count: n:a 4, n:c 3, n:b 2
func newDegreeFakeService(t *testing.T) *LanternService {
	t.Helper()
	fb := newFakeBackend()
	for _, k := range []string{"n:a", "n:b", "n:c", "n:d", "x:1", "x:2"} {
		fb.vertices[k] = &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}}
	}
	put := func(tail, head string, w float32) {
		if fb.edges[tail] == nil {
			fb.edges[tail] = map[string]float32{}
		}
		fb.edges[tail][head] = w
	}
	put("n:a", "n:b", 1)
	put("n:a", "n:c", 2)
	put("n:a", "x:1", 4)
	put("n:b", "n:c", 1)
	put("x:2", "n:c", 8)
	put("x:2", "n:a", 1)
	return NewLanternService(fb)
}

func TestTopVerticesByDegree(t *testing.T) {
	ctx := context.Background()

	t.Run("OutByCount", func(t *testing.T) {
		svc := newDegreeFakeService(t)
		resp, err := svc.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 2, Direction: pb.TopVerticesByDegreeRequest_OUT,
		})
		if err != nil {
			t.Fatalf("TopVerticesByDegree: %v", err)
		}
		if len(resp.Entries) != 2 {
			t.Fatalf("entries = %d, want 2", len(resp.Entries))
		}
		if resp.Entries[0].Key != "n:a" || resp.Entries[0].Degree != 3 || resp.Entries[0].WeightedDegree != 7 {
			t.Errorf("entry[0] = %+v, want n:a 3/7", resp.Entries[0])
		}
		if resp.Entries[1].Key != "n:b" || resp.Entries[1].Degree != 1 {
			t.Errorf("entry[1] = %+v, want n:b degree 1", resp.Entries[1])
		}
	})

	t.Run("InByWeight", func(t *testing.T) {
		svc := newDegreeFakeService(t)
		resp, err := svc.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 1, Direction: pb.TopVerticesByDegreeRequest_IN, Weighted: true,
		})
		if err != nil {
			t.Fatalf("TopVerticesByDegree: %v", err)
		}
		if len(resp.Entries) != 1 || resp.Entries[0].Key != "n:c" || resp.Entries[0].WeightedDegree != 11 {
			t.Fatalf("in-by-weight top1 = %+v, want n:c weight 11", resp.Entries)
		}
		if resp.Entries[0].Degree != 3 {
			t.Errorf("n:c in-degree = %d, want 3", resp.Entries[0].Degree)
		}
	})

	t.Run("BothByCount", func(t *testing.T) {
		svc := newDegreeFakeService(t)
		resp, err := svc.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 3, Direction: pb.TopVerticesByDegreeRequest_BOTH,
		})
		if err != nil {
			t.Fatalf("TopVerticesByDegree: %v", err)
		}
		gotKeys := []string{resp.Entries[0].Key, resp.Entries[1].Key, resp.Entries[2].Key}
		if want := []string{"n:a", "n:c", "n:b"}; gotKeys[0] != want[0] || gotKeys[1] != want[1] || gotKeys[2] != want[2] {
			t.Fatalf("both keys = %v, want %v", gotKeys, want)
		}
		if resp.Entries[0].Degree != 4 || resp.Entries[1].Degree != 3 || resp.Entries[2].Degree != 2 {
			t.Errorf("both degrees = %d,%d,%d, want 4,3,2",
				resp.Entries[0].Degree, resp.Entries[1].Degree, resp.Entries[2].Degree)
		}
	})

	t.Run("UnspecifiedDirectionDefaultsToOut", func(t *testing.T) {
		svc := newDegreeFakeService(t)
		resp, err := svc.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{Prefix: "n:", K: 1})
		if err != nil {
			t.Fatalf("TopVerticesByDegree: %v", err)
		}
		// Out-degree leader is n:a (3); the in-degree leader would be n:c (3
		// with a higher weight), so this distinguishes the default axis.
		if len(resp.Entries) != 1 || resp.Entries[0].Key != "n:a" {
			t.Fatalf("unspecified default top1 = %+v, want n:a (out-degree)", resp.Entries)
		}
	})

	t.Run("EmptyPrefixRejected", func(t *testing.T) {
		svc := newDegreeFakeService(t)
		var reason string
		svc.onValidationReject = func(r string) { reason = r }
		_, err := svc.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{Prefix: "", K: 5})
		if err == nil {
			t.Fatal("empty prefix accepted; want InvalidArgument")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("empty-prefix code = %v, want InvalidArgument", connect.CodeOf(err))
		}
		if reason != "empty_prefix" {
			t.Errorf("reject reason = %q, want empty_prefix", reason)
		}
	})

	t.Run("KDefaultAndClamp", func(t *testing.T) {
		svc := newDegreeFakeService(t)
		svc.scan = ScanLimits{ScanDefaultLimit: 2, ScanMaxLimit: 3}

		// k == 0 falls back to ScanDefaultLimit (2).
		def, err := svc.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 0, Direction: pb.TopVerticesByDegreeRequest_BOTH,
		})
		if err != nil {
			t.Fatalf("k=0: %v", err)
		}
		if len(def.Entries) != 2 {
			t.Errorf("k=0 returned %d entries, want ScanDefaultLimit=2", len(def.Entries))
		}

		// An oversized k is capped at ScanMaxLimit (3).
		capped, err := svc.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 100, Direction: pb.TopVerticesByDegreeRequest_BOTH,
		})
		if err != nil {
			t.Fatalf("k=100: %v", err)
		}
		if len(capped.Entries) != 3 {
			t.Errorf("k=100 returned %d entries, want ScanMaxLimit=3", len(capped.Entries))
		}
	})
}
