package dataruntime

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

// tarpSearchRefresh owns the derived database for one selected Graph
// runtime. Only this worker advances, builds, or retires bases.
type tarpSearchRefresh struct {
	relay     *tarpRelay
	derived   *tarp.DB
	config    tarpSearchStartup
	directory string

	active       uint64
	pending      uint64 // one unconfirmed build or obsolete base awaiting proof
	cleanupAfter uint64
	cleanupDone  bool

	cancel context.CancelFunc
	done   chan struct{}
}

// StartDiskSearch opens the owned derived index before request admission.
func (r *ServingRuntime) StartDiskSearch(ctx context.Context, directory string, positions bool, limits search.SearchAnalysisLimits, project func(string, *pb.Vertex) search.Document) error {
	if r == nil || r.tarp == nil || r.tarp.searchRefresh != nil || r.closed.Load() {
		return errTarpSearchCut
	}
	if err := r.tarp.ensureTarpSearchSource(ctx); err != nil {
		return err
	}
	options := []search.IndexOption{search.WithAnalysisLimits(limits)}
	if positions {
		options = append(options, search.WithPositions())
	}
	config := tarpSearchStartup{
		corpus:         "vertices",
		fingerprint:    query.IndexFormatFingerprint(r.tarp.namespace, positions, limits),
		project:        project,
		analyzer:       search.NewScriptAwareAnalyzer(),
		scorer:         search.DefaultLexicalScorer(),
		options:        options,
		maxChangeBytes: 8 << 20,
		maxChangeKeys:  4096,
		lazyBoot:       true,
	}
	const startupAttempt = 2 * time.Second
	startupCtx, cancel := context.WithTimeout(ctx, startupAttempt)
	derived, err := openTarpSearchStore(startupCtx, directory)
	var id uint64
	if err == nil {
		id, err = r.tarp.openDiskSearch(startupCtx, derived, config)
	}
	cancel()
	if ctx.Err() != nil {
		if derived != nil {
			return errors.Join(ctx.Err(), derived.Close())
		}
		return ctx.Err()
	}
	if err != nil {
		if !errors.Is(err, search.ErrIndexIncomplete) && !errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("Tarp Search startup deferred", "error", err)
		}
		r.tarp.diskSearch = &tarpDiskSearchReader{
			project: project, maxChangeBytes: config.maxChangeBytes, maxChangeKeys: config.maxChangeKeys,
		}
		r.tarp.trackSearch = true
	}
	r.tarp.diskAdmission = true
	r.startSearchWorker(derived, config, id, directory, 50*time.Millisecond, startupAttempt)
	return nil
}

func openTarpSearchStore(ctx context.Context, directory string) (*tarp.DB, error) {
	return tarp.OpenContext(ctx, directory, tarp.Options{
		OpenValidation:         tarp.Indexed,
		BTreeMemoryBudgetBytes: 16 << 20,
		ValueCacheBudgetBytes:  16 << 20,
		ReadFileHandles:        32,
		MaxRecordBytes:         4 << 20,
	})
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
	r.startSearchWorker(derived, config, id, "", interval, attempt)
	return nil
}

func (r *ServingRuntime) startSearchWorker(derived *tarp.DB, config tarpSearchStartup, id uint64, directory string, interval, attempt time.Duration) {
	workerCtx, cancel := context.WithCancel(context.Background())
	worker := &tarpSearchRefresh{
		relay: r.tarp, derived: derived, config: config, active: id,
		directory: directory, cancel: cancel, done: make(chan struct{}),
	}
	r.tarp.searchRefresh = worker
	go worker.run(workerCtx, interval, attempt)
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
			err := w.refreshWithin(refreshCtx, ctx)
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
	if w.derived == nil {
		return nil
	}
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

func (w *tarpSearchRefresh) refreshWithin(ctx, rebuildCtx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.derived == nil {
		var err error
		w.derived, err = openTarpSearchStore(rebuildCtx, w.directory)
		if err != nil {
			return err
		}
	}
	view, err := w.readGraph(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = view.Close() }()
	input, err := captureTarpDiskSearchInput(ctx, view, w.config.corpus, w.config.fingerprint, time.Now(), w.config.project)
	if err != nil {
		return err
	}
	if err := w.resolvePending(ctx, view); err != nil {
		return err
	}
	ref, err := readTarpSearchBaseRef(ctx, view)
	if err != nil && !errors.Is(err, tarp.ErrNotFound) && !errors.Is(err, errTarpSearchCut) {
		return err
	}
	reader := w.relay.diskSearch
	selected := reader.base.Load()
	if selected == nil && w.active == 0 && ref.ID != 0 {
		id, index, err := reusableTarpSearchBase(ctx, view, input, w.derived, w.config)
		if err != nil && !errors.Is(err, search.ErrIndexIncomplete) {
			return err
		}
		if index != nil {
			w.active = id
			reader.base.Store(index)
			selected = index
		}
	}
	certified := selected != nil && selected.BaseID() == ref.ID && sameTarpDiskSource(selected.Source(), ref.Source) &&
		ref.Source.Epoch == input.source.Epoch
	if certified && reader.badBase.CompareAndSwap(selected, nil) {
		reader.base.CompareAndSwap(selected, nil)
		return w.rebuild(rebuildCtx, input, w.active)
	}
	if !certified {
		reader.base.Store(nil)
		return w.rebuild(rebuildCtx, input, w.active)
	}
	if input.changeCut.first <= ref.Source.Sequence {
		// Reclamation may have stopped at the previous attempt deadline.
		// Retry it even if no new graph change or deadline will advance B.
		if err := reclaimTarpSearchChanges(ctx, w.relay.db, ref); err != nil {
			return err
		}
	}
	err = reader.advance(ctx, w.relay.db, w.derived, selected.BaseID(), w.config.analyzer, w.config.scorer, search.Budget{}, w.config.options...)
	if err == nil {
		return w.reclaimAbandonedBase(ctx)
	}
	if ctx.Err() != nil {
		return err
	}
	// The same immutable base cannot advance through a suffix exceeding
	// these work limits. Rebuild from the bounded Graph stream instead.
	if errors.Is(err, search.ErrBudgetExceeded) || errors.Is(err, errTarpSearchCut) {
		return w.rebuild(rebuildCtx, input, ref.ID)
	}
	if _, openErr := search.OpenDiskIndex(ctx, w.derived, ref.ID, ref.Source, w.config.analyzer, w.config.scorer, w.config.options...); isDerivedSearchFault(openErr) {
		reader.base.Store(nil)
		return w.rebuild(rebuildCtx, input, ref.ID)
	}
	return err
}

// Sweep abandoned startup/refresh generations one base at a time. In-process
// replacement already retires its previous base through pending; the sweep
// also handles artifacts left before a crash. The worker owns all selectors.
func (w *tarpSearchRefresh) reclaimAbandonedBase(ctx context.Context) error {
	if w.cleanupDone {
		return nil
	}
	id, err := search.NextDiskBase(ctx, w.derived, w.cleanupAfter)
	if err != nil {
		return err
	}
	if id == 0 {
		w.cleanupDone = true
		return nil
	}
	if id != w.active && id != w.pending {
		if err := search.RetireDiskBase(ctx, w.derived, id); err != nil {
			return err
		}
	}
	w.cleanupAfter = id
	return nil
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
