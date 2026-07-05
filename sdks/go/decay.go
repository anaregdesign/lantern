package client

import (
	"context"
	"fmt"
	"math"
	"time"
)

// MaxDecaySteps is the default per-call fan-out ceiling for AddDecayingEdge /
// DecayContributions: a single decaying add expands into at most this many
// additive contributions. It is a safety rail, not the fundamental limit —
// the binding constraints are the server's LANTERN_TOMBSTONE_TTL horizon
// (which caps Steps*Interval) and float32 underflow. Geometric decay with
// Ratio <= 0.5 is already negligible (below 2^-16 of the initial weight) by 16
// steps; callers who want a smoother or longer curve raise Interval / the
// half-life rather than the step count. Kept a named constant so the ceiling
// can move without an API change.
const MaxDecaySteps = 16

// DecayOpts specifies a geometric ("exponential") decay staircase for the
// live weight contributed by one AddDecayingEdge call. The contributed live
// weight starts at InitialWeight and is multiplied by Ratio every Interval,
// reaching exactly zero after Steps intervals. It is realized as up to Steps
// additive edge contributions with staggered TTLs; no server-side support is
// required (see DecayContributions for the exact expansion).
type DecayOpts struct {
	// InitialWeight is S(0): the live-sum weight this call contributes to the
	// edge immediately after it is applied. It may be negative (a decaying
	// negative reinforcement) but must be non-zero.
	InitialWeight float32
	// Ratio is the per-step multiplier r, which must lie in the open interval
	// (0, 1): the contributed live weight on step k is InitialWeight*r^k.
	Ratio float32
	// Steps is the number of decay steps N; it must satisfy
	// 1 <= Steps <= MaxDecaySteps. Steps == 1 degenerates to a single
	// AddEdge(InitialWeight) expiring after Interval.
	Steps int
	// Interval is the wall-clock duration of one decay step; it must be > 0.
	Interval time.Duration
}

// validate reports the first way opts is ill-formed, or nil when it is a
// well-formed decay spec. Errors wrap ErrInvalidArgument so callers can match
// them with errors.Is, mirroring the server's InvalidArgument mapping for the
// server-side checks (expiration horizon, capacity) that only surface on the
// wire.
func (o DecayOpts) validate() error {
	switch {
	case o.InitialWeight == 0:
		return fmt.Errorf("%w: DecayOpts.InitialWeight must be non-zero", ErrInvalidArgument)
	case !(o.Ratio > 0 && o.Ratio < 1):
		return fmt.Errorf("%w: DecayOpts.Ratio must be in the open interval (0, 1); got %v", ErrInvalidArgument, o.Ratio)
	case o.Steps < 1:
		return fmt.Errorf("%w: DecayOpts.Steps must be >= 1; got %d", ErrInvalidArgument, o.Steps)
	case o.Steps > MaxDecaySteps:
		return fmt.Errorf("%w: DecayOpts.Steps must be <= MaxDecaySteps (%d); got %d", ErrInvalidArgument, MaxDecaySteps, o.Steps)
	case o.Interval <= 0:
		return fmt.Errorf("%w: DecayOpts.Interval must be > 0; got %v", ErrInvalidArgument, o.Interval)
	}
	return nil
}

// DecayContributions expands a geometric decay spec into the additive edge
// contributions that realize it, relative to base as t=0. It is the pure,
// deterministic core of AddDecayingEdge: contribution j (1-indexed) targets
// the same (tail, head), expires at base+j*Interval, and carries the step
// drop
//
//	c_j = S(j-1) - S(j)   for j = 1..N-1
//	c_N = S(N-1)          (the residual, folded into the last step)
//
// where S(k) = InitialWeight * Ratio^k is the target live-sum on step k and
// N = Steps. Because a read sums every live contribution, these telescope to
// S(0) = InitialWeight: the edge's live weight is InitialWeight right after
// the add and then follows the staircase InitialWeight, InitialWeight*Ratio,
// …, and exactly 0 once the last contribution expires at base+N*Interval.
//
// Note the contribution weights are the step DROPS, not the live-sum values:
// InitialWeight=16, Ratio=0.5, Steps=5 yields weights 8,4,2,1,1 (not
// 16,8,4,2,1, whose live sum would start at 31). Specifying the target curve
// rather than the raw weights is the whole point of the helper.
//
// Contributions that underflow to exactly zero in float32 (deep in a fast
// decay) are omitted — they would add nothing while still auto-materializing
// endpoints and consuming server capacity — so the returned slice may be
// shorter than Steps. The math is done in float64 and rounded once to float32
// per contribution, so a caller comparing the read-back live weight against
// InitialWeight should allow a small float32 tolerance.
//
// It returns an error wrapping ErrInvalidArgument when opts is ill-formed, or
// when InitialWeight is so small the entire curve underflows float32 to zero.
func DecayContributions(tail, head string, opts DecayOpts, base time.Time) ([]EdgeInput, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	w0 := float64(opts.InitialWeight)
	r := float64(opts.Ratio)
	out := make([]EdgeInput, 0, opts.Steps)
	for j := 1; j <= opts.Steps; j++ {
		// c_j is the drop S(j-1)-S(j) = w0*r^(j-1)*(1-r) for every step but
		// the last, which carries the whole residual S(N-1) = w0*r^(N-1) so
		// the live weight reaches exactly zero at base+N*Interval.
		exp := float64(j - 1)
		var c float64
		if j < opts.Steps {
			c = w0 * math.Pow(r, exp) * (1 - r)
		} else {
			c = w0 * math.Pow(r, exp)
		}
		w := float32(c)
		if w == 0 {
			continue
		}
		out = append(out, EdgeInput{
			Tail:       tail,
			Head:       head,
			Weight:     w,
			Expiration: base.Add(time.Duration(j) * opts.Interval),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf(
			"%w: DecayOpts curve underflows float32 to zero (InitialWeight %v is too small)",
			ErrInvalidArgument, opts.InitialWeight)
	}
	return out, nil
}

// HalfLifeDecay builds a DecayOpts from a half-life instead of an explicit
// ratio: the contributed weight halves every halfLife, sampled every interval
// over the given horizon. It sets
//
//	Ratio = 2^(-interval/halfLife)
//	Steps = ceil(horizon/interval), clamped to [1, MaxDecaySteps]
//
// The result is fed to AddDecayingEdge / DecayContributions, which validate
// it; HalfLifeDecay itself does not error, so a non-positive
// halfLife/interval/horizon yields a DecayOpts that those functions reject.
func HalfLifeDecay(initialWeight float32, halfLife, interval, horizon time.Duration) DecayOpts {
	opts := DecayOpts{InitialWeight: initialWeight, Interval: interval}
	if halfLife > 0 && interval > 0 {
		opts.Ratio = float32(math.Pow(0.5, float64(interval)/float64(halfLife)))
	}
	if interval > 0 {
		steps := int(math.Ceil(float64(horizon) / float64(interval)))
		switch {
		case steps < 1:
			steps = 1
		case steps > MaxDecaySteps:
			steps = MaxDecaySteps
		}
		opts.Steps = steps
	}
	return opts
}

// AddDecayingEdge accumulates a single edge whose contributed live weight
// follows the geometric decay staircase described by opts, using time.Now()
// as the t=0 reference. It expands opts (see DecayContributions) into up to
// opts.Steps additive contributions and applies them in one AddEdges batch,
// so it inherits AddEdges' automatic chunking, WithIdempotentAdds dedup
// (#588), retry policy, and post-accumulation effective-weight reporting
// (#897).
//
// It returns the edge's effective (live-sum) weight immediately after the
// add — any preexisting live weight on (tail, head) plus opts.InitialWeight.
//
// The whole curve is a single AddEdges request (Steps <= MaxDecaySteps is far
// below the batch chunk size), so the server validates every contribution's
// expiration before applying any: a horizon longer than the server's
// LANTERN_TOMBSTONE_TTL rejects the entire add with ErrInvalidArgument rather
// than writing a truncated staircase. A later PutEdge or DeleteEdge on the
// same endpoints replaces or removes every contribution, discarding whatever
// remains of the schedule.
func (l *Lantern) AddDecayingEdge(ctx context.Context, tail, head string, opts DecayOpts) (float32, error) {
	inputs, err := DecayContributions(tail, head, opts, time.Now())
	if err != nil {
		return 0, err
	}
	effective, err := l.AddEdges(ctx, inputs)
	if err != nil {
		return 0, err
	}
	// The contributions share (tail, head) and are applied in order, so the
	// last entry's effective weight is the full post-add live sum.
	if len(effective) == 0 {
		return 0, nil
	}
	return effective[len(effective)-1], nil
}
