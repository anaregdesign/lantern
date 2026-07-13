package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// runSearchExamples is the maintained SearchVertices contract example. It is
// called by main after the sample vertices exist, so ordinary `go build` keeps
// one-shot, paging, cancellation, and incremental API usage compiling.
func runSearchExamples(ctx context.Context, cli *client.Lantern) error {
	status, err := cli.GetServerStatus(ctx)
	if err != nil {
		return fmt.Errorf("discover search capabilities: %w", err)
	}
	search := status.GetSearch()
	log.Printf("search enabled=%t positions=%t analyzer=%s projection=%s fingerprint=%s",
		search.GetEnabled(), search.GetPositionsEnabled(), search.GetAnalyzerVersion(),
		search.GetProjectionVersion(), search.GetConfigFingerprint())

	// One-shot namespace search. A disabled index is a calm, typed state rather
	// than an error string applications need to parse.
	hits, err := cli.SearchVertices(ctx, "A",
		client.WithSearchPrefix("string"),
		client.WithMatchMode(client.MatchAll),
		client.WithSearchLimit(10),
	)
	if errors.Is(err, client.ErrSearchDisabled) {
		log.Printf("search unavailable: %s", client.SearchFailureReason(err))
		return nil
	}
	if err != nil {
		return fmt.Errorf("one-shot search: %w", err)
	}
	log.Printf("one-shot search returned %d hits", len(hits))

	// Phrase is issued only when the endpoint advertises positions. Fuzziness
	// is a separate request because phrase and expansion options do not compose.
	if search.GetPositionsEnabled() {
		if _, err := cli.SearchVertices(ctx, "quiet cafe", client.WithPhrase()); err != nil {
			return fmt.Errorf("phrase search: %w", err)
		}
	}
	if _, err := cli.SearchVertices(ctx, "serach", client.WithFuzziness(1)); err != nil {
		return fmt.Errorf("typo search: %w", err)
	}

	// The iterator follows the endpoint-sticky bounded snapshot lazily and
	// requests exact value/TTL snapshots with each ranked hit.
	for hit, err := range cli.SearchVerticesIter(ctx, "A",
		client.WithSearchLimit(2), client.WithFullVertex()) {
		if err != nil {
			return fmt.Errorf("search pagination: %w", err)
		}
		log.Printf("paged search hit key=%s score=%f status=%s", hit.Key, hit.Score, hit.ProjectionStatus)
	}

	// Per-call cancellation is terminal and never returns a partial page.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := cli.SearchVertices(cancelled, "A"); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("cancelled search returned unexpected error: %w", err)
	}

	// IncrementalSearch debounces inputs, cancels superseded RPCs, and only
	// publishes the newest query. Its options are the same one-shot options.
	incrementalCtx, stopIncremental := context.WithCancel(ctx)
	incremental := cli.NewIncrementalSearch(incrementalCtx,
		client.WithDebounce(0),
		client.WithIncrementalSearchLimit(10),
		client.WithIncrementalSearchOptions(client.WithPrefixTerms()),
	)
	incremental.Search("lan")
	select {
	case update := <-incremental.Updates():
		if update.Err != nil {
			stopIncremental()
			_ = incremental.Close()
			return fmt.Errorf("incremental search: %w", update.Err)
		}
		log.Printf("incremental query=%q hits=%d", update.Query, len(update.Hits))
	case <-time.After(2 * time.Second):
		stopIncremental()
		_ = incremental.Close()
		return errors.New("incremental search timed out")
	}
	stopIncremental()
	return incremental.Close()
}
