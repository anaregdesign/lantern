package dataruntime

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A Snapshot sends application rows from one immutable generation. The
// publication gate is held only while pinning and registering the tail.
type tarpSnapshotReader struct {
	view      *graphstore.ReadView
	rows      *graphstore.Rows[string, *pb.Vertex]
	now       time.Time
	format    pb.SnapshotFormat
	namespace string
	bounds    receiptSnapshotCausalBounds
}

func (s *Replication) streamTarpSnapshot(ctx context.Context, format pb.SnapshotFormat, reserve func(uint64, map[string]uint64, <-chan struct{}) error, stream Sender[pb.SnapshotResponse]) error {
	owner, ok := s.origins.(*Executor)
	if !ok || owner.runtime != s.runtime {
		return errors.New("Tarp Snapshot requires its serving publication owner")
	}
	rows, err := diskRows()
	if err != nil {
		return err
	}
	reader := tarpSnapshotReader{rows: rows, format: format, namespace: s.namespaceFormat}
	defer func() {
		if reader.view != nil {
			_ = reader.view.Close()
		}
	}()
	var origins []OriginState
	var header pb.SnapshotHeader
	header.Format = format
	header.NamespaceFormat = s.namespaceFormat
	err = owner.withExclusiveCommittedView(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		view, err := s.runtime.tarp.db.Read()
		if err != nil {
			return err
		}
		reader.view = view
		reader.now = time.Now()
		durable, err := s.runtime.tarp.readCut(view.Snapshot, view.Generation)
		if err != nil {
			return err
		}
		local, _ := s.log.LastSeq()
		if local != durable.last {
			return errors.New("Snapshot relay position differs from its committed cut")
		}
		header.CutoffLocalSeq = local
		origins = s.origins.OriginStates()
		header.CutoffSeqPerOrigin = make(map[string]uint64, len(origins))
		for _, o := range origins {
			header.CutoffSeqPerOrigin[hex.EncodeToString(o.Origin[:])] = o.LastSeq
		}
		if durable.receipts {
			ledger, err := readTarpReceiptCut(view.Snapshot, view.Generation)
			if err != nil {
				return err
			}
			if err = s.clock.RestoreFloor(hlc.Timestamp{WallNs: ledger.highWater * int64(time.Millisecond), NodeID: s.clock.NodeID()}); err != nil {
				return err
			}
		}
		ts := s.clock.Now()
		header.CutoffHlc = hlcToProto(ts)
		if reserve != nil {
			return reserve(local, maps.Clone(header.CutoffSeqPerOrigin), owner.publicationFaultCh)
		}
		return nil
	})
	if err != nil {
		return err
	}
	footer := &pb.SnapshotFooter{}
	hdr := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Header{Header: &header}}
	end := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Footer{Footer: footer}}
	var ledger *tarpReceiptTable
	if format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
		ledger, err = openTarpReceiptTable(ctx, reader.view.Generation, reader.view.Snapshot, s.receiptSnapshotPolicy)
		if err != nil {
			return err
		}
		if err = ledger.prepareFrames(ctx, &header, footer, origins); err != nil {
			return err
		}
		reader.bounds.cutoff = hlcFromProto(header.CutoffHlc)
		reader.bounds.originLast, err = validateReceiptSnapshotOrigins(&header, origins, reader.bounds.cutoff)
		if err != nil {
			return err
		}
		// Fail before the header on invalid stored graph rows. The second
		// pass walks exactly the same immutable rows and expiry sample.
		if err = reader.body(ctx, func(*pb.SnapshotResponse) error { return nil }); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stream.Send(hdr); err != nil {
		return err
	}
	if ledger != nil {
		_, err = ledger.scan(ctx, func(_ mutationreceipt.Epoch, rec mutationreceipt.Receipt) error {
			f, err := ledger.frame(rec)
			if err != nil {
				return err
			}
			return stream.Send(f)
		})
		if err != nil {
			return err
		}
	}
	err = reader.body(ctx, func(frame *pb.SnapshotResponse) error {
		switch frame.Entry.(type) {
		case *pb.SnapshotResponse_VertexCausalBarrier:
			footer.VertexCausalBarrierCount++
		case *pb.SnapshotResponse_EdgeCausalBarrier:
			footer.EdgeCausalBarrierCount++
		case *pb.SnapshotResponse_VertexTombstone:
			footer.VertexTombstoneCount++
		case *pb.SnapshotResponse_EdgeTombstone:
			footer.EdgeTombstoneCount++
		case *pb.SnapshotResponse_EdgeContributionTombstone:
			footer.EdgeContributionTombstoneCount++
		case *pb.SnapshotResponse_Vertex:
			footer.VertexCount++
		case *pb.SnapshotResponse_Edge:
			footer.EdgeCount++
		}
		return stream.Send(frame)
	})
	if err != nil {
		return err
	}
	return stream.Send(end)
}

func (r *tarpSnapshotReader) body(ctx context.Context, send func(*pb.SnapshotResponse) error) error {
	emit := func(f *pb.SnapshotResponse) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.namespace != "" {
			if err := validatePhysicalDataIdentities(f.ProtoReflect()); err != nil {
				return err
			}
		}
		if r.format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
			if err := ValidateReceiptSnapshotFrame(f); err != nil {
				return err
			}
		}
		return send(f)
	}
	gen, snap := r.view.Generation, r.view.Snapshot
	for kind := graphstore.VertexBarrier; kind <= graphstore.ContributionTombstone; kind++ {
		floors := r.rows.FloorsOfKind(ctx, snap, gen, kind)
		if kind == graphstore.VertexBarrier || kind == graphstore.EdgeBarrier {
			floors = mergeSnapshotFloors(floors, r.implicitFloors(ctx, kind))
		}
		for row, err := range floors {
			if err != nil {
				return err
			}
			if !cache.IsLiveAt(row.Floor.Expiration, r.now) {
				continue
			}
			k, h := row.Key, hlcToProto(row.Floor.HLC)
			if r.format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
				if _, ok := r.bounds.parse(h); !ok {
					return errors.New("invalid Snapshot floor HLC")
				}
			}
			var f pb.SnapshotResponse
			switch kind {
			case graphstore.VertexBarrier:
				f.Entry = &pb.SnapshotResponse_VertexCausalBarrier{VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: k.Key, Hlc: h}}
			case graphstore.EdgeBarrier:
				f.Entry = &pb.SnapshotResponse_EdgeCausalBarrier{EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{Tail: k.Tail, Head: k.Head, Hlc: h}}
			case graphstore.VertexTombstone:
				f.Entry = &pb.SnapshotResponse_VertexTombstone{VertexTombstone: &pb.SnapshotVertexTombstone{Key: k.Key, Hlc: h, Expiration: timestamppb.New(row.Floor.Expiration)}}
			case graphstore.EdgeTombstone:
				f.Entry = &pb.SnapshotResponse_EdgeTombstone{EdgeTombstone: &pb.SnapshotEdgeTombstone{Tail: k.Tail, Head: k.Head, Hlc: h, Expiration: timestamppb.New(row.Floor.Expiration)}}
			case graphstore.ContributionTombstone:
				f.Entry = &pb.SnapshotResponse_EdgeContributionTombstone{EdgeContributionTombstone: &pb.SnapshotEdgeContributionTombstone{Tail: k.Tail, Head: k.Head, ContribId: k.ContribID[:], Hlc: h, Expiration: timestamppb.New(row.Floor.Expiration)}}
			}
			if err := emit(&f); err != nil {
				return err
			}
		}
	}
	for row, err := range r.rows.Vertices(ctx, snap, gen, "", nil, false) {
		if err != nil {
			return err
		}
		v := row.Vertex
		if !cache.IsLiveAt(v.Expiration, r.now) {
			continue
		}
		data := replicationSnapshotVertex(graphcache.SnapshotVertex[string, *pb.Vertex]{Key: row.Key, Value: v.Value, Expiration: v.Expiration, HLC: v.HLC})
		if data.GetKey() != row.Key {
			return errors.New("stored Vertex identity differs from Snapshot row")
		}
		item := &pb.SnapshotVertex{Vertex: data, Hlc: hlcToProto(v.HLC)}
		if r.format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
			if !validReceiptSnapshotVertexValue(data) || !validOptionalReceiptSnapshotTimestamp(data.GetExpiration()) {
				return errors.New("invalid stored Snapshot Vertex")
			}
			if item.Hlc != nil {
				if _, ok := r.bounds.parse(item.Hlc); !ok {
					return errors.New("invalid stored Snapshot Vertex HLC")
				}
			}
		}
		if err = emit(&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Vertex{Vertex: item}}); err != nil {
			return err
		}
	}
	for row, err := range r.rows.Edges(ctx, snap, gen, "", "", nil, false) {
		if err != nil {
			return err
		}
		f, err := r.edge(ctx, row)
		if err != nil {
			return err
		}
		if f != nil {
			if err = emit(f); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (r *tarpSnapshotReader) implicitFloors(ctx context.Context, kind graphstore.FloorKind) iter.Seq2[graphstore.FloorEntry[string], error] {
	return func(yield func(graphstore.FloorEntry[string], error) bool) {
		gen, snap := r.view.Generation, r.view.Snapshot
		if kind == graphstore.VertexBarrier {
			for row, err := range r.rows.Vertices(ctx, snap, gen, "", nil, false) {
				if err != nil {
					yield(graphstore.FloorEntry[string]{}, err)
					return
				}
				if cache.IsLiveAt(row.Vertex.Expiration, r.now) || row.Vertex.HLC == (hlc.Timestamp{}) {
					continue
				}
				if !yield(graphstore.FloorEntry[string]{Key: graphstore.FloorKey[string]{Kind: kind, Key: row.Key}, Floor: graphstore.Floor{HLC: row.Vertex.HLC}}, nil) {
					return
				}
			}
			return
		}
		for row, err := range r.rows.Edges(ctx, snap, gen, "", "", nil, false) {
			if err != nil {
				yield(graphstore.FloorEntry[string]{}, err)
				return
			}
			if row.Edge.HLC == (hlc.Timestamp{}) {
				continue
			}
			k := graphstore.FloorKey[string]{Kind: kind, Tail: row.Key.Tail, Head: row.Key.Head}
			if !yield(graphstore.FloorEntry[string]{Key: k, Floor: graphstore.Floor{HLC: row.Edge.HLC}}, nil) {
				return
			}
		}
	}
}

// Merge explicit expired-Put history with floors still held by committed
// Vertex/Edge rows. Both iterators use ordered physical identity keys.
func mergeSnapshotFloors(stored, derived iter.Seq2[graphstore.FloorEntry[string], error]) iter.Seq2[graphstore.FloorEntry[string], error] {
	return func(yield func(graphstore.FloorEntry[string], error) bool) {
		a, stopA := iter.Pull2(stored)
		defer stopA()
		b, stopB := iter.Pull2(derived)
		defer stopB()
		x, errA, hasA := a()
		y, errB, hasB := b()
		for hasA || hasB {
			if err := errors.Join(errA, errB); err != nil {
				yield(graphstore.FloorEntry[string]{}, err)
				return
			}
			order := cmp.Or(cmp.Compare(x.Key.Key, y.Key.Key), cmp.Compare(x.Key.Tail, y.Key.Tail), cmp.Compare(x.Key.Head, y.Key.Head))
			switch {
			case !hasB || hasA && order < 0:
				if !yield(x, nil) {
					return
				}
				x, errA, hasA = a()
			case !hasA || order > 0:
				if !yield(y, nil) {
					return
				}
				y, errB, hasB = b()
			default:
				if x.Floor.HLC.Less(y.Floor.HLC) {
					x = y
				}
				if !yield(x, nil) {
					return
				}
				x, errA, hasA = a()
				y, errB, hasB = b()
			}
		}
	}
}

func (r *tarpSnapshotReader) edge(ctx context.Context, row graphstore.EdgeEntry[string]) (*pb.SnapshotResponse, error) {
	gen, snap := r.view.Generation, r.view.Snapshot
	k := row.Key
	if r.namespace == "" {
		for _, endpoint := range []string{k.Tail, k.Head} {
			_, live, err := r.rows.VertexAt(snap, gen, endpoint, r.now)
			if err != nil || !live {
				return nil, err
			}
		}
	}
	e := graphcache.SnapshotEdge[string]{Tail: k.Tail, Head: k.Head, HLC: row.Edge.HLC}
	floor, ok, err := r.rows.GetFloor(snap, gen, graphstore.FloorKey[string]{Kind: graphstore.EdgeBarrier, Tail: k.Tail, Head: k.Head})
	if err != nil {
		return nil, err
	}
	if ok && e.HLC.Less(floor.HLC) {
		e.HLC = floor.HLC
	}
	appendSource := func(id graphcache.ContribID, s graphstore.Source) {
		if cache.IsLiveAt(s.Expiration, r.now) {
			e.Contributions = append(e.Contributions, graphcache.SnapshotContribution{ContribID: id, HLC: s.HLC, Weight: s.Weight, Expiration: s.Expiration, DerivedAggregate: s.DerivedAggregate})
		}
	}
	if row.Edge.Base != nil {
		appendSource(graphcache.ContribID{}, *row.Edge.Base)
	}
	for source, err := range r.rows.Sources(ctx, snap, gen, k) {
		if err != nil {
			return nil, err
		}
		appendSource(source.ID.ContribID, source.Source)
	}
	if len(e.Contributions) == 0 {
		return nil, nil
	}
	f, err := snapshotEdgeFrame(e, r.format, r.namespace)
	if err != nil {
		return nil, err
	}
	if r.format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
		edge := f.GetEdge()
		slices.SortFunc(edge.Contributions, func(a, b *pb.SnapshotEdgeContribution) int { return bytes.Compare(a.GetContribId(), b.GetContribId()) })
		floor, found, err := r.rows.GetFloor(snap, gen, graphstore.FloorKey[string]{Kind: graphstore.EdgeTombstone, Tail: k.Tail, Head: k.Head})
		if err != nil {
			return nil, err
		}
		var tomb hlc.Timestamp
		if found && cache.IsLiveAt(floor.Expiration, r.now) {
			tomb = floor.HLC
		}
		for _, v := range edge.Contributions {
			if len(v.ContribId) == 0 {
				continue
			}
			floor, found, err := r.rows.GetFloor(snap, gen, graphstore.FloorKey[string]{Kind: graphstore.ContributionTombstone, Tail: k.Tail, Head: k.Head, ContribID: contribIDFromBytes(v.ContribId)})
			if err != nil {
				return nil, err
			}
			if found && cache.IsLiveAt(floor.Expiration, r.now) {
				return nil, fmt.Errorf("Snapshot contains a deleted contribution on %q -> %q", k.Tail, k.Head)
			}
		}
		if _, err := validateReceiptSnapshotEdge(edge, tomb, nil, r.bounds, true); err != nil {
			return nil, err
		}
	}
	return f, nil
}
