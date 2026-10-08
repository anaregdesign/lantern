package security

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"sort"
	"time"
)

// CatchUp transfers only authenticated CHOSEN messages into an intact owner.
// It never downloads P/B families or uses another node's materialized cut as
// authority. One bounded range RPC per owner is separate from delivery slots.
func (o *s3aOwner) CatchUp(ctx context.Context, peer uint32) (uint64, error) {
	if !o.enterCall() {
		return 0, errS3AClosed
	}
	defer o.calls.Done()
	if err := o.check(ctx); err != nil {
		return 0, err
	}
	select {
	case o.rangeSlot <- struct{}{}:
		defer func() { <-o.rangeSlot }()
	default:
		return 0, errS3ACredit
	}
	ctx, cancel := context.WithTimeout(ctx, o.limits.RPCTimeout)
	defer cancel()
	stop := context.AfterFunc(o.ctx, cancel)
	defer stop()
	_, receipt, err := o.kernel.ReadLocalCut()
	if err != nil {
		if errors.Is(err, errS2CUnknown) || errors.Is(err, errS2CClosed) {
			o.fail()
		}
		return 0, err
	}
	from := receipt.ControlSlot + 1
	request := binary.BigEndian.AppendUint64(nil, from)
	request = binary.BigEndian.AppendUint64(request, o.limits.RangeSlots)
	limit := o.limits.RangeBytes + 4*o.limits.RangeSlots + uint64(len(s3aRangeMagic)+4)
	raw, status, admission, err := o.requestAdmitted(ctx, peer, s3aRangePath, request, limit)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, errS3AWire
	}
	parts, err := o.decodeRange(raw, from)
	if err != nil {
		return 0, err
	}
	var applied uint64
	for _, part := range parts {
		if err := o.receive(ctx, peer, part, func() error { return admission.Check(ctx) }); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

func (o *s3aOwner) decodeRange(raw []byte, from uint64) ([][]byte, error) {
	header := len(s3aRangeMagic) + 4
	if len(raw) < header || string(raw[:len(s3aRangeMagic)]) != s3aRangeMagic {
		return nil, errS3AWire
	}
	count := uint64(binary.BigEndian.Uint32(raw[len(s3aRangeMagic):header]))
	if count > o.limits.RangeSlots || from == 0 || from > ^uint64(0)-count {
		return nil, errS3AWire
	}
	parts := make([][]byte, 0, count)
	rest, total := raw[header:], uint64(0)
	for i := uint64(0); i < count; i++ {
		if len(rest) < 4 {
			return nil, errS3AWire
		}
		n := uint64(binary.BigEndian.Uint32(rest[:4]))
		rest = rest[4:]
		if n == 0 || n > uint64(len(rest)) || n > o.kernel.trust.bounds.PayloadBytes || n > o.limits.RangeBytes-total {
			return nil, errS3AWire
		}
		part := rest[:n]
		m, err := s2cDecodeMessage(o.kernel.trust, part)
		if err != nil || m.kind != s2cChosen || m.slot != from+i {
			return nil, errS3AWire
		}
		parts = append(parts, part)
		total += n
		rest = rest[n:]
	}
	if len(rest) != 0 {
		return nil, errS3AWire
	}
	return parts, nil
}

// Drive attempts one next slot in a finite scheduling window. It starts a real
// phase 1, retries exact retained bytes, polls bounded chosen ranges in rotating
// order, and paces escalation. There is no chosen-value oracle or new H issuer.
// Success proves a local installed slot, not current policy freshness/admission.
func (o *s3aOwner) Drive(ctx context.Context, candidate [32]byte) (s2LocalReceipt, error) {
	if !o.enterCall() {
		return s2LocalReceipt{}, errS3AClosed
	}
	defer o.calls.Done()
	select {
	case o.driveSlot <- struct{}{}:
		defer func() { <-o.driveSlot }()
	default:
		return s2LocalReceipt{}, errS3ACredit
	}
	if err := o.check(ctx); err != nil {
		return s2LocalReceipt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.limits.ScheduleWindow)
	defer cancel()
	stop := context.AfterFunc(o.ctx, cancel)
	defer stop()
	_, initial, err := o.kernel.ReadLocalCut()
	if err != nil {
		return s2LocalReceipt{}, err
	}
	ids := make([]uint32, 0, len(o.peers)-1)
	for id := range o.peers {
		if id != o.kernel.config.Member {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	nextBallot := time.Time{}
	for round := 0; round < o.limits.Rounds; round++ {
		if !time.Now().Before(nextBallot) {
			err = o.Begin(ctx, candidate)
			if err == nil {
				nextBallot = time.Now().Add(o.limits.BallotBackoff)
			}
		} else {
			err = o.Retry(ctx)
		}
		if err != nil && !errors.Is(err, errS3ACredit) && !errors.Is(err, errS2CCapacity) && !errors.Is(err, errS2CProtocol) {
			return s2LocalReceipt{}, err
		}
		if !s3aPause(ctx, o.limits.Backoff) {
			return s2LocalReceipt{}, ctx.Err()
		}
		for offset := range ids {
			_, _ = o.CatchUp(ctx, ids[(round+offset)%len(ids)])
		}
		_, receipt, err := o.kernel.ReadLocalCut()
		if err != nil {
			if errors.Is(err, errS2CUnknown) || errors.Is(err, errS2CClosed) {
				o.fail()
			}
			return s2LocalReceipt{}, err
		}
		if receipt.ControlSlot > initial.ControlSlot {
			if err := o.check(ctx); err != nil {
				return s2LocalReceipt{}, err
			}
			return receipt, nil
		}
	}
	return s2LocalReceipt{}, errS3ACredit
}
