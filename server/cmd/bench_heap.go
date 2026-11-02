//go:build lantern_bench_heap && (linux || darwin)

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"

	"github.com/anaregdesign/lantern/server/benchheap"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

// Fixed in a measurement build. Only the explicitly controlled test binary's
// dual-tag *_test.go may override this path for its owned temporary directory.
var benchHeapDirectory = benchheap.Directory

// The owner may observe context cancellation before the error channel. Retain
// the first refusal until both workers have joined so shutdown cannot hide it.
type benchHeapRefusal struct {
	once sync.Once
	err  error
}

func (r *benchHeapRefusal) record(reason string, publish func() error, notify chan<- error, cancel context.CancelFunc) {
	r.once.Do(func() {
		r.err = errors.Join(errors.New("heap observation refused: "+reason), publish())
		notify <- r.err
		cancel()
	})
}

func startBenchHeapObservation(ctx context.Context, a *App) (func() error, <-chan error, error) {
	store, err := benchheap.OpenStore(benchHeapDirectory, benchHeapPublish)
	if err != nil {
		return nil, nil, err
	}
	var session benchheap.Session
	if err := store.Read(benchheap.SessionFile, &session, benchheap.MaxInputBytes); err != nil {
		store.Close()
		return nil, nil, err
	}
	if session.Validate() != nil || session.UID != os.Geteuid() {
		store.Close()
		return nil, nil, benchheap.ErrInput
	}
	binary, err := os.Executable()
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	file, err := os.Open(binary)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	err = errors.Join(readErr, file.Close())
	if err != nil || hex.EncodeToString(hash.Sum(nil)) != session.BinarySHA256 {
		store.Close()
		return nil, nil, benchheap.ErrInput
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		store.Close()
		return nil, nil, err
	}
	identity := benchheap.Identity{Session: session, PID: os.Getpid(), StartNonce: hex.EncodeToString(nonce[:])}
	observer, err := dataruntime.NewBenchHeapObserver(a.svc)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	if err := a.server.ObserveBenchHeap(observer); err != nil {
		observer.Close()
		store.Close()
		return nil, nil, err
	}
	a.peerServer.ObserveBenchHeap(observer)
	owned, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal, 1)
	usr1, _ := benchheap.Signal()
	signal.Notify(signals, usr1)
	if err := store.Publish(benchheap.ReadyFile, benchheap.Ready{Schema: benchheap.Schema, Protocol: benchheap.Protocol, Identity: identity}); err != nil {
		signal.Stop(signals)
		cancel()
		observer.Close()
		store.Close()
		return nil, nil, err
	}
	errorsOut := make(chan error, 1)
	joined := make(chan struct{})
	signalJoined := make(chan struct{})
	requests := make(chan struct{}, 1)
	var busy atomic.Bool
	var refused benchHeapRefusal
	refuse := func(reason string) {
		refused.record(reason, func() error {
			record := struct {
				Schema   int                `json:"schema"`
				Protocol string             `json:"protocol"`
				Identity benchheap.Identity `json:"identity"`
				Reason   string             `json:"reason"`
			}{benchheap.Schema, benchheap.Protocol, identity, reason}
			return store.Publish(benchheap.RefusalFile, record)
		}, errorsOut, cancel)
	}
	// Keep consuming signal notifications while Collect blocks. A second signal
	// during an admitted operation is a failure, never a queued extra GC cut.
	go func() {
		defer close(signalJoined)
		for {
			select {
			case <-owned.Done():
				return
			case <-signals:
				if !busy.CompareAndSwap(false, true) {
					refuse("overlap")
					return
				}
				select {
				case requests <- struct{}{}:
				case <-owned.Done():
					return
				}
			}
		}
	}()
	go func() {
		defer close(joined)
		defer func() { cancel(); <-signalJoined }()
		ordinal := 1
		var previousCut string
		for {
			select {
			case <-owned.Done():
				return
			case <-requests:
				if ordinal > 2 {
					refuse("extra_operation")
					return
				}
				phase := "baseline"
				if ordinal == 2 {
					phase = "post"
				}
				var request benchheap.Request
				if store.Read(benchheap.RequestFile(phase), &request, benchheap.MaxInputBytes) != nil || request.Validate(identity, ordinal) != nil || request.CutNonce == previousCut {
					refuse("request")
					return
				}
				if err := store.Publish(benchheap.StartedFile(phase), benchheap.Started{Request: request}); err != nil {
					refuse("started_io")
					return
				}
				out := benchheap.Observe(owned, request, observer.Snapshot, benchheap.RuntimeDriver())
				if out.Status == benchheap.StatusComplete {
					// The next signal may arrive as soon as rename exposes completion,
					// while Publish is still syncing its directory. Admit it now; this
					// single worker cannot execute it until publication succeeds.
					previousCut = request.CutNonce
					ordinal++
					busy.Store(false)
				}
				if err := store.Publish(benchheap.CompletionFile(phase), out); err != nil {
					refuse("completion_io")
					return
				}
				if out.Status != benchheap.StatusComplete {
					refuse("incomplete")
					return
				}
			}
		}
	}()
	var once sync.Once
	var closeErr error
	stop := func() error {
		once.Do(func() {
			cancel()
			signal.Stop(signals)
			<-joined
			closeErr = errors.Join(refused.err, observer.Close(), store.Close())
		})
		return closeErr
	}
	return stop, errorsOut, nil
}
