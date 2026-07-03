package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// TopVerticesByDegree ranks the most-connected live vertices whose key starts
// with the request prefix by their degree, returning the top k in descending
// order of the ranking metric (#900). A non-empty prefix is REQUIRED — there is
// no whole-graph ranking, so an empty prefix is rejected with InvalidArgument.
//
// k is clamped with the shared scan knobs: zero falls back to ScanDefaultLimit
// and any value is capped at ScanMaxLimit. Direction selects out / in / both
// degree (DIRECTION_UNSPECIFIED defaults to out-degree); Weighted ranks by the
// summed live edge weight instead of the raw live edge count. Both the count
// and the weighted sum are always returned per entry regardless of which one
// ranked the result.
//
// Counts honour the live-visibility rule (#750): expired vertices and fully
// decayed edges do not contribute. The result is point-in-time best-effort,
// like GetServerStatus counts. This is a read-only aggregate — it logs no
// mutation and is not replicated.
func (s *LanternService) TopVerticesByDegree(ctx context.Context, in *pb.TopVerticesByDegreeRequest) (*pb.TopVerticesByDegreeResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if in.GetPrefix() == "" {
		if s.onValidationReject != nil {
			s.onValidationReject("empty_prefix")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("prefix is required"))
	}
	start := time.Now()
	k := clampLimit(in.GetK(), s.scan.ScanDefaultLimit, s.scan.ScanMaxLimit)

	dir := degreeDirection(in.GetDirection())
	ranked := s.cache.TopVerticesByDegree(in.GetPrefix(), int(k), dir, in.GetWeighted())

	// The backend orders survivors by the ranking metric descending but breaks
	// ties arbitrarily (its key type is only comparable). Impose a total order
	// here — metric desc, then key ascending — so the wire output is stable for
	// a given cache state.
	sort.SliceStable(ranked, func(i, j int) bool {
		var mi, mj float64
		if in.GetWeighted() {
			mi, mj = ranked[i].WeightedDegree, ranked[j].WeightedDegree
		} else {
			mi, mj = float64(ranked[i].Degree), float64(ranked[j].Degree)
		}
		if mi != mj {
			return mi > mj
		}
		return ranked[i].Key < ranked[j].Key
	})

	entries := make([]*pb.TopVerticesByDegreeResponse_Entry, len(ranked))
	for i, e := range ranked {
		entries[i] = &pb.TopVerticesByDegreeResponse_Entry{
			Key:            e.Key,
			Degree:         e.Degree,
			WeightedDegree: e.WeightedDegree,
		}
	}
	s.metrics.OnScan("TopVerticesByDegree", len(entries), time.Since(start))
	return &pb.TopVerticesByDegreeResponse{Entries: entries}, nil
}

// degreeDirection maps the request Direction enum to the core degree axis,
// treating DIRECTION_UNSPECIFIED as out-degree — the cheapest well-defined
// default and the one the store's DocLen counter is framed around.
func degreeDirection(d pb.TopVerticesByDegreeRequest_Direction) graphcache.DegreeDirection {
	switch d {
	case pb.TopVerticesByDegreeRequest_DIRECTION_IN:
		return graphcache.DegreeIn
	case pb.TopVerticesByDegreeRequest_DIRECTION_BOTH:
		return graphcache.DegreeBoth
	default:
		return graphcache.DegreeOut
	}
}
