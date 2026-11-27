package dataruntime

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/tarp"
)

// tarpSearchRefresh owns the derived database for one selected Graph
// runtime. Only this worker advances, builds, or retires bases.
type tarpSearchRefresh struct {
	relay   *tarpRelay
	derived *tarp.DB
	config  tarpSearchStartup

	active  uint64
	pending uint64 // one unconfirmed build or obsolete base awaiting proof

	cancel context.CancelFunc
	done   chan struct{}
}

// startOwnedDiskSearch takes ownership of derived only on success. Call before
// admitting requests. The startup context bounds startup, not the worker's
// lifetime. Close cancels and joins it before closing Graph.
func (r *ServingRuntime) startOwnedDiskSearch(ctx context.Context, derived *tarp.DB, config tarpSearchStartup, interval, attempt time.Duration) error {
	if r == nil || r.tarp == nil || r.tarp.searchRefresh != nil || r.closed.Load() || interval <= 0 || attempt <= 0 {
		return errTarpSearchCut
	}
	config.options = slices.Clone(config.options)
	id, err := r.tarp.openDiskSearch(ctx, derived, config)
	if err != nil {
		return err
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	worker := &tarpSearchRefresh{
		relay: r.tarp, derived: derived, config: config, active: id,
		cancel: cancel, done: make(chan struct{}),
	}
	r.tarp.searchRefresh = worker
	go worker.run(workerCtx, interval, attempt)
	return nil
}

func (w *tarpSearchRefresh) run(ctx context.Context, interval, attempt time.Duration) {
	defer close(w.done)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	delay, ceiling := interval, max(interval, attempt)
	var lastFailure string
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			refreshCtx, cancel := context.WithTimeout(ctx, attempt)
			err := w.refresh(refreshCtx)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				lastFailure = ""
				delay = interval
			} else {
				if failure := err.Error(); failure != lastFailure {
					slog.Warn("Tarp Search refresh deferred", "error", err)
					lastFailure = failure
				}
				if delay < ceiling/2 {
					delay *= 2
				} else {
					delay = ceiling
				}
			}
			timer.Reset(delay)
		}
	}
}

func (w *tarpSearchRefresh) stop() {
	w.cancel()
	<-w.done
}

func (w *tarpSearchRefresh) closeDerived() error {
	reader := w.relay.diskSearch
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.base.Store(nil)
	return w.derived.Close()
}

func (w *tarpSearchRefresh) readGraph(ctx context.Context) (*graphstore.ReadView, error) {
	view, err := w.relay.db.Read()
	if err != nil {
		return nil, err
	}
	_, err = readTarpSearchCut(ctx, view.Snapshot, view.Generation)
	if err == nil {
		return view, nil
	}
	_ = view.Close()
	if !errors.Is(err, tarp.ErrNotFound) {
		return nil, err
	}
	// A true incoming graph replacement omitted the local Search source.
	// Initialize a new epoch from the currently committed Graph relay.
	if err := w.relay.ensureTarpSearchSource(ctx); err != nil {
		return nil, err
	}
	return w.relay.db.Read()
}

func (w *tarpSearchRefresh) refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	view, err := w.readGraph(ctx)
	if err != nil {
		return err
	}
	defer view.Close()
	input, err := captureTarpDiskSearchInput(ctx, view, w.config.corpus, w.config.fingerprint, time.Now(), w.config.project)
	if err != nil {
		return err
	}
	if err := w.resolvePending(ctx, view); err != nil {
		return err
	}
	ref, err := readTarpSearchBaseRef(ctx, view)
	if err != nil && !errors.Is(err, tarp.ErrNotFound) {
		return err
	}
	reader := w.relay.diskSearch
	selected := reader.base.Load()
	certified := selected != nil && selected.BaseID() == ref.ID && sameTarpDiskSource(selected.Source(), ref.Source) &&
		ref.Source.Epoch == input.source.Epoch
	if !certified {
		reader.base.Store(nil)
		return w.rebuild(ctx, input, w.active)
	}
	if input.changeCut.first <= ref.Source.Sequence {
		// Reclamation may have stopped at the previous attempt deadline.
		// Retry it even if no new graph change or deadline will advance B.
		if err := reclaimTarpSearchChanges(ctx, w.relay.db, ref); err != nil {
			return err
		}
	}
	err = reader.advance(ctx, w.relay.db, w.derived, selected.BaseID(), w.config.analyzer, w.config.scorer, search.Budget{}, w.config.options...)
	if err == nil || ctx.Err() != nil {
		return err
	}
	// An explicitly bounded planning failure leaves the existing base ready
	// and lags Search behind a proven overlay. Only a failed derived reopen
	// proves the current base needs corrective reconstruction.
	if _, openErr := search.OpenDiskIndex(ctx, w.derived, ref.ID, ref.Source, w.config.analyzer, w.config.scorer, w.config.options...); errors.Is(openErr, search.ErrDiskIndex) {
		reader.base.Store(nil)
		return w.rebuild(ctx, input, ref.ID)
	}
	return err
}

func (w *tarpSearchRefresh) resolvePending(ctx context.Context, graph *graphstore.ReadView) error {
	if w.pending == 0 {
		return nil
	}
	ref, err := readTarpSearchBaseRef(ctx, graph)
	if err != nil && !errors.Is(err, tarp.ErrNotFound) {
		return err
	}
	if err == nil && ref.ID == w.pending {
		// An earlier Graph selector commit may have returned an uncertain
		// outcome. Reopen exactly what Graph names before any retirement.
		selected, err := openSelectedTarpSearchBase(ctx, graph, w.derived, w.config.analyzer, w.config.scorer, w.config.options...)
		if err != nil {
			return err
		}
		old := w.active
		w.active, w.pending = ref.ID, 0
		if old != ref.ID {
			w.pending = old
		}
		w.relay.diskSearch.base.Store(selected)
		return nil
	}
	if err := search.RetireDiskBase(ctx, w.derived, w.pending); err != nil {
		return err
	}
	w.pending = 0
	return nil
}

func (w *tarpSearchRefresh) rebuild(ctx context.Context, input tarpDiskSearchInput, old uint64) error {
	if w.pending != 0 {
		// Preserve a finite amount of staging after ambiguous selection or
		// retirement. Do not allocate another generation until it is resolved.
		return search.ErrIndexIncomplete
	}
	id, err := reserveTarpSearchBase(ctx, w.derived)
	if err != nil {
		return err
	}
	w.pending = id
	if err := input.build(ctx, w.derived, id, w.config.analyzer, w.config.options...); err != nil {
		return err
	}
	reader := w.relay.diskSearch
	ref := tarpSearchBaseRef{ID: id, Source: input.source}
	reader.mu.Lock()
	if err := selectTarpSearchBase(ctx, w.relay.db, w.derived, ref, w.config.analyzer, w.config.scorer,
		w.config.maxChangeBytes, w.config.maxChangeKeys, w.config.options...); err != nil {
		reader.mu.Unlock()
		return err
	}
	selected, err := search.OpenDiskIndex(ctx, w.derived, id, ref.Source, w.config.analyzer, w.config.scorer, w.config.options...)
	if err != nil {
		reader.mu.Unlock()
		return err
	}
	w.active, w.pending = id, old
	reader.base.Store(selected)
	reader.mu.Unlock()
	return reclaimTarpSearchChanges(ctx, w.relay.db, ref)
}
