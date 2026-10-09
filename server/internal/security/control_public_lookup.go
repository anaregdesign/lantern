package security

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

const (
	currentOriginalPath         = "/lantern-private/control/v2/original"
	currentOriginalRequestLimit = 2048
)

type currentOriginalQuery struct {
	ID     FullChangeID
	Intent [32]byte
}

// Only the existing authenticated workload transport reaches this handler.
// Empty means this participant has no exact retained H, never safe nonexecution.
// One existing per-peer ingress credit bounds the H copy; no user credentials,
// purpose proof, ledger scan response or new timestamp crosses this seam.
func (o *s3aOwner) originalResponse(ctx context.Context, raw []byte) ([]byte, error) {
	var q currentOriginalQuery
	if len(raw) > currentOriginalRequestLimit || s2StrictJSON(raw, &q, DefaultPolicyLimits()) != nil || !q.ID.valid() || q.Intent == [32]byte{} {
		return nil, errS3AWire
	}
	k := o.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	cut := k.trust.genesis.state.projection.cut
	if ctx.Err() != nil || k.readyLocked() != nil || q.ID.Domain != cut.Domain || q.ID.Cohort != cut.Cohort {
		return nil, ErrAuthorityUnavailable
	}
	for _, h := range k.origins {
		if h.handoff.id == q.ID {
			if h.handoff.operation.digest != q.Intent {
				return nil, ErrChangeConflict
			}
			return []byte(h.raw), nil
		}
	}
	return nil, nil
}

// findOriginal is a bounded read of enrolled peers. Historical verification is
// independent of the responding peer; an empty/failed response cannot authorize
// a fresh consume. Each owner has one outbound lookup slot, independently of its
// renewal/delivery/range slots, and no lookup runs on the warm data path.
func (o *s3aOwner) findOriginal(ctx context.Context, id FullChangeID, intent [32]byte) (*s2cHistoricalH, error) {
	if !o.enterCall() {
		return nil, errS3AClosed
	}
	defer o.calls.Done()
	select {
	case o.originalSlot <- struct{}{}:
		defer func() { <-o.originalSlot }()
	default:
		return nil, errS3ACredit
	}
	ctx, cancel := context.WithTimeout(ctx, o.limits.ScheduleWindow)
	defer cancel()
	raw, err := json.Marshal(currentOriginalQuery{id, intent})
	if err != nil || len(raw) > currentOriginalRequestLimit || !id.valid() || intent == [32]byte{} {
		return nil, errS3AWire
	}
	ids := make([]uint32, 0, len(o.peers)-1)
	for peer := range o.peers {
		if peer != o.kernel.config.Member {
			ids = append(ids, peer)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, peer := range ids {
		response, status, _, err := o.requestAdmitted(ctx, peer, currentOriginalPath, raw, o.kernel.trust.bounds.HistoricalBytes)
		if ctx.Err() != nil {
			return nil, errors.Join(errS2CUnknown, ctx.Err())
		}
		if err != nil || status != http.StatusOK {
			continue
		}
		h, err := verifyHistoricalH(o.kernel.trust, response)
		if err != nil || h.handoff.id != id || h.handoff.operation.digest != intent {
			continue
		}
		return h, nil
	}
	return nil, errS2CUnknown
}

// Lookup may retain a verified exact original from another enrolled node and
// complete that historical candidate and catch up authenticated chosen history.
// Neither a peer's claimed status nor
// local absence is elevated into an original Apply result. Disclosure still
// requires a fresh current admission at the service output boundary.
func (o *CurrentAuthority) Lookup(ctx context.Context, profile CurrentProfile, id FullChangeID, intent [32]byte) (CurrentChangeResult, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return CurrentChangeResult{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	r, err := o.Inspect(ctx, profile, id, intent)
	if err != nil || r.Progress == CurrentApplied {
		return r, err
	}
	if r.Progress == CurrentUnresolved {
		h, err := o.origin.network.findOriginal(ctx, id, intent)
		if err != nil {
			return r, err
		}
		if _, err = o.origin.carryOriginal(ctx, []byte(h.raw)); err != nil {
			return r, err
		}
	}
	// Status-first recovery must also finish an H which became durable before
	// its original invocation could enter Drive. Select only the exact verified
	// historical candidate; status never prepares, consumes or remints work.
	n := o.origin.network
	ctx, cancel := context.WithTimeout(ctx, n.limits.ScheduleWindow)
	defer cancel()
	candidate := func() [32]byte {
		k := n.kernel
		k.gate.Lock()
		defer k.gate.Unlock()
		defer k.poisonPanic()
		if k.readyLocked() != nil {
			return [32]byte{}
		}
		for digest, h := range k.origins {
			if h.handoff.id == id && h.handoff.operation.digest == intent {
				return digest
			}
		}
		if h := k.chosen; h != nil && h.h != nil && h.h.handoff.id == id && h.h.handoff.operation.digest == intent {
			return h.h.digest()
		}
		return [32]byte{}
	}()
	if candidate != [32]byte{} {
		for ctx.Err() == nil {
			if _, err = n.Drive(ctx, candidate); err != nil {
				break
			}
			r, err = o.Inspect(ctx, profile, id, intent)
			if err != nil || r.Progress == CurrentApplied {
				return r, err
			}
		}
	} else {
		for peer := range n.peers {
			if peer != n.kernel.config.Member {
				_, _ = n.CatchUp(ctx, peer)
			}
		}
	}
	latest, inspectErr := o.Inspect(context.WithoutCancel(ctx), profile, id, intent)
	if inspectErr == nil {
		r = latest
	}
	return r, errors.Join(err, ctx.Err(), inspectErr)
}
