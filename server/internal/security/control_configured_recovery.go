package security

import (
	"context"
	"encoding/json"
	"net/http"
)

const configuredRecoveryPath = "/lantern-private/control/v1/accepted-recovery"
const configuredRecoveryRequestLimit = 4096

type configuredRecoveryRequest struct {
	Scope  [32]byte
	Slot   uint64
	Prefix [32]byte
}

// Private read-only scheduling evidence. This returns only this participant's
// exact durably retained ACCEPT at the requested next prefix. It cannot grant
// serving authority or install a peer's projection, vote, or claimed outcome.
func (o *s3aOwner) configuredRecoveryResponse(ctx context.Context, raw []byte, check func() error) ([]byte, error) {
	var request configuredRecoveryRequest
	if len(raw) > configuredRecoveryRequestLimit || s2StrictJSON(raw, &request, DefaultPolicyLimits()) != nil || request.Slot == 0 || request.Prefix == [32]byte{} || check == nil {
		return nil, errS3AWire
	}
	k := o.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if ctx.Err() != nil || check() != nil || k.readyLocked() != nil || request.Scope != k.trust.scope {
		return nil, ErrAuthorityUnavailable
	}
	if k.chosen != nil || request.Slot != k.replayState.slot+1 || request.Prefix != k.replayState.prefix || k.accepted == nil {
		return nil, nil
	}
	accepted := k.accepted
	if accepted.kind != s2cAccept || accepted.slot != request.Slot || accepted.predecessor != request.Prefix || uint64(len(accepted.raw)) > o.frameBytes() {
		return nil, errS3AWire
	}
	return []byte(accepted.raw), nil
}

// An authenticated retained header only wakes ordinary phase one. Decode and
// full P1/H verification precede the exact-prefix recheck; this path deliberately
// never calls Receive, LearnChosen, preview, Apply, or a signing operation.
func (o *s3aOwner) configuredRecoveryHint(ctx context.Context, peer uint32) (*s2cMessage, error) {
	if !o.enterCall() {
		return nil, errS3AClosed
	}
	defer o.calls.Done()
	// Share the existing bounded original-query slot: the new permanent worker
	// does not add another simultaneous historical-proof allocation or lookup.
	select {
	case o.originalSlot <- struct{}{}:
		defer func() { <-o.originalSlot }()
	default:
		return nil, errS3ACredit
	}
	k := o.kernel
	k.gate.Lock()
	if k.readyLocked() != nil || k.chosen != nil {
		k.gate.Unlock()
		return nil, ErrAuthorityUnavailable
	}
	request := configuredRecoveryRequest{k.trust.scope, k.replayState.slot + 1, k.replayState.prefix}
	k.gate.Unlock()
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, errS3AWire
	}
	response, status, admission, err := o.requestAdmitted(ctx, peer, configuredRecoveryPath, raw, o.frameBytes())
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent && len(response) == 0 {
		return nil, nil
	}
	if status != http.StatusOK || len(response) == 0 || admission == nil || admission.Check(ctx) != nil {
		return nil, errS3AWire
	}
	message, err := s2cDecodeMessage(k.trust, response)
	if err != nil || message.kind != s2cAccept || message.slot != request.Slot || message.predecessor != request.Prefix || s2cVerifyP1(k.trust, message) != nil {
		return nil, errS3AWire
	}
	k.gate.Lock()
	defer k.gate.Unlock()
	if ctx.Err() != nil || k.readyLocked() != nil || k.chosen != nil || k.replayState.slot+1 != request.Slot || k.replayState.prefix != request.Prefix {
		return nil, ErrRevisionConflict
	}
	return message, nil
}
