package dataruntime

import (
	"bytes"
	"errors"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

type relayOrigin struct {
	Digest [32]byte
	Origin hlc.NodeID
	Seq    uint64
}

func retainedOrigin(entry mutationlog.Entry) (hlc.NodeID, uint64, bool) {
	if ref, ok := entry.Op.(relayOrigin); ok {
		return ref.Origin, ref.Seq, ref.Origin != (hlc.NodeID{}) && ref.Seq != 0
	}
	m, ok := graphMutationFromLog(entry.Op)
	if !ok || len(m.GetOrigin()) != len(hlc.NodeID{}) {
		return hlc.NodeID{}, 0, false
	}
	return hlc.NodeID(m.GetOrigin()), m.GetSeq(), true
}

func (r *tarpRelay) Reference(entry mutationlog.Entry) mutationlog.MutationOp {
	if ref, ok := entry.Op.(relayOrigin); ok {
		return ref
	}
	id, seq, ok := retainedOrigin(entry)
	if !ok || r.lastReference.Origin != id || r.lastReference.Seq != seq {
		panic("dataruntime: committed Tarp relay lost its validated origin")
	}
	return r.lastReference
}

type tarpReplayView struct {
	view  *graphstore.ReadView
	owner *tarpRelay
}

func (r *tarpRelay) Pin() (mutationlog.RetainedReader, error) {
	if fault := r.replayReadFault.Load(); fault != nil {
		return nil, *fault
	}
	v, err := r.db.Read()
	if err != nil {
		r.replayReadFault.CompareAndSwap(nil, &err)
		return nil, err
	}
	return &tarpReplayView{view: v, owner: r}, nil
}

func (r *tarpReplayView) Close() error { return r.view.Close() }

func (r *tarpReplayView) Read(ref mutationlog.Entry) (result mutationlog.Entry, readErr error) {
	defer func() {
		if readErr != nil {
			r.owner.replayReadFault.CompareAndSwap(nil, &readErr)
		}
	}()
	raw, err := r.view.Snapshot.Get(tarpEntryKey(r.view.Generation, ref.Seq))
	if err != nil {
		return mutationlog.Entry{}, err
	}
	if len(raw) <= 32 {
		return mutationlog.Entry{}, errors.New("truncated Tarp replay entry")
	}
	identity, ok := ref.Op.(relayOrigin)
	if !ok || tarpEntryDigest([32]byte(raw[:32]), ref.Seq, raw[32:]) != identity.Digest {
		return mutationlog.Entry{}, errors.New("Tarp replay payload differs from committed digest")
	}
	op, err := decodeReceiptWALUnion(raw[32:])
	if err != nil {
		return mutationlog.Entry{}, err
	}
	m, ok := graphMutationFromLog(op)
	origin, seq, valid := retainedOrigin(ref)
	if !ok || !valid || m.GetSeq() != seq || !bytes.Equal(m.GetOrigin(), origin[:]) || !hlcFromProto(m.GetHlc()).Equal(ref.HLC) || m.GetNamespaceFormat() != r.owner.namespace {
		return mutationlog.Entry{}, errors.New("Tarp replay entry differs from committed identity")
	}
	ref.Op = op
	return ref, nil
}
