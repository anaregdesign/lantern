package security

import "context"

// Exact recovery/status input for private composition after process restart.
// This is not current disclosure authority or a proof of global absence.
func (o *authorityOriginOwner) lookupOriginal(ctx context.Context, id FullChangeID, operation OperationIdentity) (authorityOriginResult, error) {
	if o == nil || !o.network.enterCall() {
		return authorityOriginResult{}, errS3AClosed
	}
	defer o.network.calls.Done()
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if ctx.Err() != nil || k.readyLocked() != nil {
		return authorityOriginResult{}, ErrAuthorityUnavailable
	}
	if result, known, err := o.knownLocked(&authorityOriginRequest{owner: o, id: id, operation: operation}); known || err != nil {
		return result, err
	}
	for digest, h := range k.origins {
		if h.handoff.id == id {
			if h.handoff.operation != operation {
				return authorityOriginResult{}, ErrChangeConflict
			}
			return authorityOriginResult{digest: digest, raw: h.raw}, nil
		}
	}
	return authorityOriginResult{}, errS2CUnknown
}

// Versioned v2 foreign-original retention. It never allocates a local origin
// serial, consumes a purpose or changes the original namespace/header/signature.
// Authentication is historical verification under the independently enrolled
// original origin key; current disclosure remains a separate operation.
func (o *s2cParticipant) forwardedHistorical(raw string) (*s2cHistoricalH, error) {
	if _, kind := o.recordGrammar(); kind != s2cPForwardedH {
		return nil, errS2CProtocol
	}
	h, err := verifyHistoricalH(o.trust, []byte(raw))
	if err != nil || h.originID == o.config.OwnedOrigin {
		return nil, errS2CProtocol
	}
	if _, duplicate := o.origins[h.digest()]; duplicate {
		return nil, errS2CProtocol
	}
	for _, old := range o.origins {
		if old.handoff.id == h.handoff.id {
			return nil, ErrChangeConflict
		}
	}
	if uint64(len(o.pending)) >= o.config.PendingCount || o.pendingBytes > o.config.PendingBytes || uint64(len(raw)) > o.config.PendingBytes-o.pendingBytes {
		return nil, errS2CCapacity
	}
	return h, nil
}

func (o *s2cParticipant) installForwardedH(h *s2cHistoricalH) {
	d := h.digest()
	o.origins[d], o.pending[d] = h, h
	o.pendingBytes += uint64(len(h.raw))
}

func (o *s2cParticipant) replayForwardedH(raw string) error {
	h, err := o.forwardedHistorical(raw)
	if err != nil {
		return err
	}
	o.installForwardedH(h)
	return nil
}

func (o *authorityOriginOwner) carryOriginal(ctx context.Context, raw []byte) (authorityOriginResult, error) {
	if o == nil || !o.network.enterCall() {
		return authorityOriginResult{}, errS3AClosed
	}
	defer o.network.calls.Done()
	if err := o.network.check(ctx); err != nil {
		return authorityOriginResult{}, err
	}
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if err := k.readyLocked(); err != nil {
		return authorityOriginResult{}, err
	}
	if k.chosen != nil {
		return authorityOriginResult{}, errS2CPending
	}
	h, err := verifyHistoricalH(k.trust, raw)
	if err != nil {
		return authorityOriginResult{}, err
	}
	if original := k.replayState.ledger[h.handoff.id]; original != nil {
		if original.operation != h.handoff.operation {
			return authorityOriginResult{}, ErrChangeConflict
		}
		return authorityOriginResult{digest: original.handoff, outcome: original}, nil
	}
	d := h.digest()
	if old, known := k.origins[d]; known {
		if old.raw != h.raw {
			return authorityOriginResult{}, ErrChangeConflict
		}
		return authorityOriginResult{digest: d, raw: old.raw}, nil
	}
	if h.originID == k.config.OwnedOrigin {
		if _, err := k.persistSealedOriginLocked(raw); err != nil {
			return authorityOriginResult{}, err
		}
	} else {
		if _, err := k.forwardedHistorical(h.raw); err != nil {
			return authorityOriginResult{}, err
		}
		if err := k.appendRecord(s2cPRecord{kind: s2cPForwardedH, raw: h.raw}, k.credit); err != nil {
			return authorityOriginResult{}, err
		}
		k.installForwardedH(h)
	}
	return authorityOriginResult{digest: d, raw: h.raw}, nil
}
