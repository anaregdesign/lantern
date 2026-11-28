package dataruntime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
)

const diskSearchBaseCounter = "lantern/search/base-id/1"

type tarpSearchStartup struct {
	corpus         string
	fingerprint    string
	project        func(string, *pb.Vertex) search.Document
	analyzer       search.Analyzer
	scorer         search.Scorer
	options        []search.IndexOption
	maxChangeBytes int
	maxChangeKeys  int
	lazyBoot       bool
}

// openDiskSearch selects the currently certified derived base or constructs a
// new base from a pinned validated source. Call before admitting requests.
func (r *tarpRelay) openDiskSearch(ctx context.Context, derived *tarp.DB, config tarpSearchStartup) (uint64, error) {
	if r.diskSearch != nil || derived == nil || config.project == nil || config.analyzer == nil ||
		config.corpus == "" || config.fingerprint == "" || config.maxChangeBytes <= 0 || config.maxChangeKeys <= 0 {
		return 0, errTarpSearchCut
	}
	if err := r.ensureTarpSearchSource(ctx); err != nil {
		return 0, err
	}
	view, err := r.db.Read()
	if err != nil {
		return 0, err
	}
	defer func() { _ = view.Close() }()
	source, err := captureTarpDiskSearchInput(ctx, view, config.corpus, config.fingerprint, time.Now(), config.project)
	if err != nil {
		return 0, err
	}
	id, base, err := reusableTarpSearchBase(ctx, view, source, derived, config)
	if err != nil && !errors.Is(err, search.ErrIndexIncomplete) {
		return 0, err
	}
	if base == nil {
		if config.lazyBoot {
			for _, err := range source.documents(ctx) {
				if err != nil {
					return 0, err
				}
				return 0, search.ErrIndexIncomplete
			}
		}
		id, base, err = r.buildTarpSearchBase(ctx, source, derived, config)
		if err != nil {
			return 0, err
		}
	}
	r.diskSearch = &tarpDiskSearchReader{project: config.project, maxChangeBytes: config.maxChangeBytes, maxChangeKeys: config.maxChangeKeys}
	r.diskSearch.base.Store(base)
	r.trackSearch = true
	return id, nil
}

func reusableTarpSearchBase(ctx context.Context, view *graphstore.ReadView, input tarpDiskSearchInput, derived *tarp.DB, config tarpSearchStartup) (uint64, *search.DiskIndex, error) {
	ref, err := readTarpSearchBaseRef(ctx, view)
	if errors.Is(err, tarp.ErrNotFound) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	base := ref.Source
	if base.Store != input.source.Store || base.Corpus != config.corpus || base.Fingerprint != config.fingerprint || base.Epoch != input.source.Epoch || base.Watermark.After(input.source.Watermark) {
		return 0, nil, search.ErrIndexIncomplete
	}
	if err := visitTarpSearchChanges(ctx, view.Snapshot, view.Generation, input.changeCut, base.Sequence, base.Digest, config.maxChangeBytes, config.maxChangeKeys, func(uint64, string) error { return nil }); err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, errors.Join(search.ErrIndexIncomplete, err)
	}
	idx, err := openSelectedTarpSearchBase(ctx, view, derived, config.analyzer, config.scorer, config.options...)
	return ref.ID, idx, err
}

func (r *tarpRelay) buildTarpSearchBase(ctx context.Context, input tarpDiskSearchInput, derived *tarp.DB, config tarpSearchStartup) (uint64, *search.DiskIndex, error) {
	id, err := reserveTarpSearchBase(ctx, derived)
	if err != nil {
		return 0, nil, err
	}
	if err := input.build(ctx, derived, id, config.analyzer, config.options...); err != nil {
		return 0, nil, err
	}
	ref := tarpSearchBaseRef{ID: id, Source: input.source}
	if err := selectTarpSearchBase(ctx, r.db, derived, ref, config.analyzer, config.scorer, config.maxChangeBytes, config.maxChangeKeys, config.options...); err != nil {
		return 0, nil, err
	}
	idx, err := search.OpenDiskIndex(ctx, derived, id, ref.Source, config.analyzer, config.scorer, config.options...)
	return id, idx, err
}

func reserveTarpSearchBase(ctx context.Context, derived *tarp.DB) (uint64, error) {
	key := []byte(diskSearchBaseCounter)
	raw, err := derived.GetContext(ctx, key)
	var id uint64
	if err == nil {
		if len(raw) != 8 {
			return 0, errTarpSearchCut
		}
		id = binary.BigEndian.Uint64(raw)
	} else if !errors.Is(err, tarp.ErrNotFound) {
		return 0, err
	}
	if id == math.MaxUint64 {
		return 0, errTarpSearchCut
	}
	id++
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return id, derived.Put(key, binary.BigEndian.AppendUint64(nil, id))
}

// ClearDiskSearchSelection invalidates the last selected base before
// serving writes with Search disabled and without recording Search changes.
func (r *ServingRuntime) ClearDiskSearchSelection(ctx context.Context) error {
	if r == nil || r.tarp == nil || r.tarp.searchRefresh != nil || r.closed.Load() {
		return errTarpSearchCut
	}
	return r.tarp.db.Update(func(gen uint64, snap *tarp.Snapshot, batch *tarp.WriteBatch) error {
		key := tarpServiceKey(gen, tarpSearchBase)
		if _, err := snap.GetContext(ctx, key); errors.Is(err, tarp.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		batch.Delete(key)
		return ctx.Err()
	})
}

// initialize an empty accepted Search change stream underneath the verified
// receiver-local relay cut. A graph generation replacement omits this row.
func (r *tarpRelay) ensureTarpSearchSource(ctx context.Context) error {
	return r.db.Update(func(gen uint64, snap *tarp.Snapshot, batch *tarp.WriteBatch) error {
		if _, err := readTarpSearchCut(ctx, snap, gen); !errors.Is(err, tarp.ErrNotFound) {
			return err
		}
		relay, err := r.readCut(snap, gen)
		if err != nil {
			return err
		}
		node := r.db.NodeID()
		digest := sha256.New()
		digest.Write([]byte("Lantern disk Search source 1"))
		digest.Write(node[:])
		digest.Write(binary.BigEndian.AppendUint64(nil, gen))
		digest.Write(relay.digest[:])
		var cut tarpSearchCut
		cut.epoch, cut.first = gen, 1
		copy(cut.digest[:], digest.Sum(nil))
		batch.Put(tarpServiceKey(gen, tarpSearchSource), cut.encode())
		return ctx.Err()
	})
}
