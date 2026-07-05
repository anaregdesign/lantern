package client

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// approxEqual reports whether a and b are within tol, the float32 rounding
// slack DecayContributions warns about (the curve is computed in float64 and
// rounded once per contribution).
func approxEqual(a, b, tol float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// TestDecayContributions pins the pure expansion: the "16 → 8,4,2,1,1 (not
// 16,8,4,2,1)" golden staircase, the telescoping-to-InitialWeight invariant,
// staggered expirations, the exact-zero residual fold, negative weights, the
// Steps==1 degenerate, and the whole-curve underflow guard.
func TestDecayContributions(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()

	t.Run("golden staircase 16 r0.5 N5 → drops 8,4,2,1,1", func(t *testing.T) {
		opts := DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: time.Second}
		got, err := DecayContributions("a", "b", opts, base)
		if err != nil {
			t.Fatalf("DecayContributions: %v", err)
		}
		wantW := []float32{8, 4, 2, 1, 1}
		if len(got) != len(wantW) {
			t.Fatalf("want %d contributions, got %d", len(wantW), len(got))
		}
		var sum float32
		for i, in := range got {
			if in.Tail != "a" || in.Head != "b" {
				t.Fatalf("contribution %d endpoints = (%q,%q), want (a,b)", i, in.Tail, in.Head)
			}
			if !approxEqual(in.Weight, wantW[i], 1e-4) {
				t.Fatalf("contribution %d weight = %v, want %v", i, in.Weight, wantW[i])
			}
			wantExp := base.Add(time.Duration(i+1) * time.Second)
			if !in.Expiration.Equal(wantExp) {
				t.Fatalf("contribution %d expiration = %v, want %v", i, in.Expiration, wantExp)
			}
			sum += in.Weight
		}
		// Telescoping: the live sum right after the add is InitialWeight (16),
		// NOT the sum of a 16,8,4,2,1 raw schedule (31).
		if !approxEqual(sum, 16, 1e-4) {
			t.Fatalf("sum of drops = %v, want 16 (the live weight at t=0)", sum)
		}
	})

	t.Run("telescoping sum equals InitialWeight", func(t *testing.T) {
		cases := []DecayOpts{
			{InitialWeight: 100, Ratio: 0.9, Steps: 16, Interval: time.Second},
			{InitialWeight: 7, Ratio: 0.3, Steps: 4, Interval: time.Minute},
			{InitialWeight: 1, Ratio: 0.5, Steps: 1, Interval: time.Second},
		}
		for _, opts := range cases {
			got, err := DecayContributions("x", "y", opts, base)
			if err != nil {
				t.Fatalf("DecayContributions(%+v): %v", opts, err)
			}
			var sum float32
			for _, in := range got {
				sum += in.Weight
			}
			// Tolerance scales with the initial magnitude — float32 rounding
			// accumulates across up to MaxDecaySteps drops.
			tol := float32(math.Abs(float64(opts.InitialWeight))) * 1e-4
			if tol < 1e-4 {
				tol = 1e-4
			}
			if !approxEqual(sum, opts.InitialWeight, tol) {
				t.Fatalf("sum for %+v = %v, want %v", opts, sum, opts.InitialWeight)
			}
		}
	})

	t.Run("Steps==1 is a single AddEdge(InitialWeight)", func(t *testing.T) {
		opts := DecayOpts{InitialWeight: 5, Ratio: 0.5, Steps: 1, Interval: 2 * time.Second}
		got, err := DecayContributions("a", "b", opts, base)
		if err != nil {
			t.Fatalf("DecayContributions: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1 contribution, got %d", len(got))
		}
		if !approxEqual(got[0].Weight, 5, 1e-6) {
			t.Fatalf("weight = %v, want 5", got[0].Weight)
		}
		if !got[0].Expiration.Equal(base.Add(2 * time.Second)) {
			t.Fatalf("expiration = %v, want base+2s", got[0].Expiration)
		}
	})

	t.Run("negative InitialWeight yields negative drops summing to it", func(t *testing.T) {
		opts := DecayOpts{InitialWeight: -16, Ratio: 0.5, Steps: 5, Interval: time.Second}
		got, err := DecayContributions("a", "b", opts, base)
		if err != nil {
			t.Fatalf("DecayContributions: %v", err)
		}
		var sum float32
		for _, in := range got {
			if in.Weight >= 0 {
				t.Fatalf("negative decay must contribute negative weights, got %v", in.Weight)
			}
			sum += in.Weight
		}
		if !approxEqual(sum, -16, 1e-4) {
			t.Fatalf("sum = %v, want -16", sum)
		}
	})

	t.Run("whole-curve underflow is rejected", func(t *testing.T) {
		opts := DecayOpts{InitialWeight: math.SmallestNonzeroFloat32, Ratio: 0.5, Steps: 5, Interval: time.Second}
		_, err := DecayContributions("a", "b", opts, base)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("want ErrInvalidArgument for underflowing curve, got %v", err)
		}
	})
}

// TestDecayOptsValidation is the rejection table for DecayContributions'
// front-door validation; every ill-formed field wraps ErrInvalidArgument so
// callers can errors.Is it, matching the server's InvalidArgument mapping.
func TestDecayOptsValidation(t *testing.T) {
	base := time.Now()
	valid := DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: time.Second}

	if _, err := DecayContributions("a", "b", valid, base); err != nil {
		t.Fatalf("valid opts rejected: %v", err)
	}

	cases := []struct {
		name string
		opts DecayOpts
	}{
		{"zero InitialWeight", DecayOpts{InitialWeight: 0, Ratio: 0.5, Steps: 5, Interval: time.Second}},
		{"ratio zero", DecayOpts{InitialWeight: 16, Ratio: 0, Steps: 5, Interval: time.Second}},
		{"ratio one", DecayOpts{InitialWeight: 16, Ratio: 1, Steps: 5, Interval: time.Second}},
		{"ratio above one", DecayOpts{InitialWeight: 16, Ratio: 1.5, Steps: 5, Interval: time.Second}},
		{"ratio negative", DecayOpts{InitialWeight: 16, Ratio: -0.5, Steps: 5, Interval: time.Second}},
		{"steps zero", DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 0, Interval: time.Second}},
		{"steps above cap", DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: MaxDecaySteps + 1, Interval: time.Second}},
		{"interval zero", DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: 0}},
		{"interval negative", DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: -time.Second}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecayContributions("a", "b", tc.opts, base)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("want ErrInvalidArgument, got %v", err)
			}
		})
	}
}

// TestHalfLifeDecay checks the half-life → ratio/steps derivation and that
// the result feeds cleanly into DecayContributions.
func TestHalfLifeDecay(t *testing.T) {
	t.Run("interval == halfLife gives ratio 0.5", func(t *testing.T) {
		opts := HalfLifeDecay(10, time.Second, time.Second, 5*time.Second)
		if !approxEqual(opts.Ratio, 0.5, 1e-6) {
			t.Fatalf("ratio = %v, want 0.5", opts.Ratio)
		}
		if opts.Steps != 5 {
			t.Fatalf("steps = %d, want 5", opts.Steps)
		}
	})

	t.Run("interval == 2*halfLife gives ratio 0.25", func(t *testing.T) {
		opts := HalfLifeDecay(10, time.Second, 2*time.Second, 4*time.Second)
		if !approxEqual(opts.Ratio, 0.25, 1e-6) {
			t.Fatalf("ratio = %v, want 0.25", opts.Ratio)
		}
		if opts.Steps != 2 {
			t.Fatalf("steps = %d, want 2", opts.Steps)
		}
	})

	t.Run("steps clamp to MaxDecaySteps", func(t *testing.T) {
		opts := HalfLifeDecay(10, time.Second, time.Second, 1000*time.Second)
		if opts.Steps != MaxDecaySteps {
			t.Fatalf("steps = %d, want clamp to %d", opts.Steps, MaxDecaySteps)
		}
	})

	t.Run("non-positive horizon clamps to 1 step", func(t *testing.T) {
		opts := HalfLifeDecay(10, time.Second, time.Second, 0)
		if opts.Steps != 1 {
			t.Fatalf("steps = %d, want 1", opts.Steps)
		}
	})

	t.Run("result validates and expands", func(t *testing.T) {
		opts := HalfLifeDecay(64, time.Minute, 30*time.Second, 3*time.Minute)
		got, err := DecayContributions("a", "b", opts, time.Now())
		if err != nil {
			t.Fatalf("DecayContributions on HalfLifeDecay opts: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("want contributions, got none")
		}
	})
}

// decayEchoClient is a fake LanternServiceClient that records AddEdges
// requests and answers with cumulative effective weights — the live sum a
// fresh edge would report as each contribution accumulates. It lets the
// AddDecayingEdge delegation test assert both the expanded wire payload and
// the returned post-add live weight without a server.
type decayEchoClient struct {
	graphv1connect.LanternServiceClient
	reqs []*pb.AddEdgesRequest
}

func (c *decayEchoClient) AddEdges(_ context.Context, req *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	c.reqs = append(c.reqs, req.Msg)
	edges := req.Msg.GetEdges()
	eff := make([]float32, len(edges))
	var running float32
	for i, e := range edges {
		running += e.GetWeight()
		eff[i] = running
	}
	return connect.NewResponse(&pb.AddEdgesResponse{
		Written:          int32(len(edges)),
		EffectiveWeights: eff,
	}), nil
}

// TestAddDecayingEdge covers the method's delegation: one AddEdges batch
// carrying the expanded staircase, and a return value equal to the post-add
// live weight (InitialWeight for a fresh edge — the "16 not 31" check at the
// SDK boundary), plus validation short-circuiting before any RPC.
func TestAddDecayingEdge(t *testing.T) {
	t.Run("sends one expanded batch and returns InitialWeight", func(t *testing.T) {
		l := mustLantern(t)
		capt := &decayEchoClient{}
		l.client = capt

		opts := DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: time.Second}
		got, err := l.AddDecayingEdge(context.Background(), "a", "b", opts)
		if err != nil {
			t.Fatalf("AddDecayingEdge: %v", err)
		}
		if len(capt.reqs) != 1 {
			t.Fatalf("want 1 AddEdges request, got %d", len(capt.reqs))
		}
		if n := len(capt.reqs[0].GetEdges()); n != 5 {
			t.Fatalf("want 5 edges in the batch, got %d", n)
		}
		if !approxEqual(got, 16, 1e-4) {
			t.Fatalf("post-add live weight = %v, want 16", got)
		}
	})

	t.Run("invalid opts short-circuit before any RPC", func(t *testing.T) {
		l := mustLantern(t)
		capt := &decayEchoClient{}
		l.client = capt

		opts := DecayOpts{InitialWeight: 16, Ratio: 2, Steps: 5, Interval: time.Second}
		if _, err := l.AddDecayingEdge(context.Background(), "a", "b", opts); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("want ErrInvalidArgument, got %v", err)
		}
		if len(capt.reqs) != 0 {
			t.Fatalf("invalid opts must not send an RPC, got %d", len(capt.reqs))
		}
	})
}
