package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// commitLossTransport drops the first successful response for one RPC after
// the server has applied it; afterCommit can perform an intervening mutation.
type commitLossTransport struct {
	inner          http.RoundTripper
	pathSuffix     string
	afterCommit    func() error
	afterCommitErr error
	attempts       atomic.Int32
	dropped        atomic.Bool
}

func (tr *commitLossTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := strings.HasSuffix(req.URL.Path, tr.pathSuffix)
	if target {
		tr.attempts.Add(1)
	}
	resp, err := tr.inner.RoundTrip(req)
	if err != nil || !target || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices ||
		!tr.dropped.CompareAndSwap(false, true) {
		return resp, err
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if tr.afterCommit != nil {
		tr.afterCommitErr = tr.afterCommit()
	}
	return nil, errors.New("injected committed response loss")
}

func TestGoSDKRetrySafety_RealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(key string) string { return key })
	svc := service.NewLanternService(cache)
	validation := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	primary := newConnectTestServer(t, svc, nil, validation.ConnectInterceptor())
	healthy := newConnectClientFor(t, primary.url)
	policy := client.WithRetry(client.RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   time.Nanosecond,
		MaxDelay:    time.Nanosecond,
	})
	seedVertex := func(t *testing.T, key string) {
		t.Helper()
		if outcome, err := healthy.PutVertex(ctx, key, key, time.Hour); err != nil ||
			outcome != client.PutOutcomeAppliedAndLive {
			t.Fatalf("seed PutVertex(%q) = (%s, %v)", key, outcome, err)
		}
	}
	lossClient := func(t *testing.T, tr *commitLossTransport) *client.Lantern {
		t.Helper()
		return newConnectClientFor(t, primary.url,
			client.WithHTTPClient(&http.Client{Transport: tr}),
			client.WithIdempotentAdds(),
			policy,
		)
	}

	t.Run("successful result-bearing calls still work", func(t *testing.T) {
		seedVertex(t, "retry/happy/1")
		seedVertex(t, "retry/happy/2")
		sdk := newConnectClientFor(t, primary.url, client.WithIdempotentAdds(), policy)
		if n, err := sdk.DeleteVerticesByPrefix(ctx, "retry/happy/", client.WithDeleteByPrefixLimit(1)); err != nil || n != 1 {
			t.Fatalf("successful capped Delete = (%d, %v), want (1, nil)", n, err)
		}
		if n, err := healthy.CountVerticesByPrefix(ctx, "retry/happy/"); err != nil || n != 1 {
			t.Fatalf("remaining vertices = (%d, %v), want (1, nil)", n, err)
		}
		if weight, err := sdk.AddEdge(ctx, "retry/happy", "edge", 2, time.Hour); err != nil || weight != 2 {
			t.Fatalf("successful Add = (%v, %v), want (2, nil)", weight, err)
		}
	})

	t.Run("eligible read still retries after response loss", func(t *testing.T) {
		seedVertex(t, "retry/read")
		tr := &commitLossTransport{inner: h2cClient().Transport, pathSuffix: "/GetVertex"}
		sdk := lossClient(t, tr)
		vertex, err := sdk.GetVertex(ctx, "retry/read")
		if err != nil || vertex.GetKey() != "retry/read" || !tr.dropped.Load() || tr.attempts.Load() != 2 {
			t.Fatalf("retried read = (%+v, %v), dropped=%t attempts=%d", vertex, err, tr.dropped.Load(), tr.attempts.Load())
		}
	})

	t.Run("exact Delete keeps original existed result unknown", func(t *testing.T) {
		seedVertex(t, "retry/exact")
		tr := &commitLossTransport{inner: h2cClient().Transport, pathSuffix: "/DeleteVertex"}
		sdk := lossClient(t, tr)
		existed, err := sdk.DeleteVertex(ctx, "retry/exact")
		if !errors.Is(err, client.ErrUnavailable) || existed || !tr.dropped.Load() || tr.attempts.Load() != 1 {
			t.Fatalf("ambiguous Delete = (%t, %v), dropped=%t attempts=%d", existed, err, tr.dropped.Load(), tr.attempts.Load())
		}
		if _, err := healthy.GetVertex(ctx, "retry/exact"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("committed Delete must remove vertex: %v", err)
		}
	})

	t.Run("plain Add cannot resurrect after response loss and Delete", func(t *testing.T) {
		tr := &commitLossTransport{inner: h2cClient().Transport, pathSuffix: "/AddEdges"}
		tr.afterCommit = func() error {
			edge, err := healthy.GetEdge(ctx, "retry/add", "edge")
			if err != nil || edge.GetWeight() != 2 {
				return fmt.Errorf("Add did not commit before response loss: edge=%+v err=%v", edge, err)
			}
			existed, err := healthy.DeleteEdge(ctx, "retry/add", "edge")
			if err != nil || !existed {
				return fmt.Errorf("intervening DeleteEdge = (%t, %v)", existed, err)
			}
			return nil
		}
		sdk := lossClient(t, tr)
		weight, err := sdk.AddEdge(ctx, "retry/add", "edge", 2, time.Hour)
		if !errors.Is(err, client.ErrUnavailable) || weight != 0 || !tr.dropped.Load() ||
			tr.attempts.Load() != 1 || tr.afterCommitErr != nil {
			t.Fatalf("ambiguous Add = (%v, %v), dropped=%t attempts=%d intervening=%v",
				weight, err, tr.dropped.Load(), tr.attempts.Load(), tr.afterCommitErr)
		}
		if _, err := healthy.GetEdge(ctx, "retry/add", "edge"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("Add replay resurrected deleted edge: %v", err)
		}
	})

	t.Run("capped vertex prefix Delete does not remove next victim", func(t *testing.T) {
		for _, key := range []string{"retry/vertex/1", "retry/vertex/2", "retry/vertex/3"} {
			seedVertex(t, key)
		}
		tr := &commitLossTransport{inner: h2cClient().Transport, pathSuffix: "/DeleteVerticesByPrefix"}
		sdk := lossClient(t, tr)
		deleted, err := sdk.DeleteVerticesByPrefix(ctx, "retry/vertex/", client.WithDeleteByPrefixLimit(1))
		if !errors.Is(err, client.ErrUnavailable) || deleted != 0 || !tr.dropped.Load() || tr.attempts.Load() != 1 {
			t.Fatalf("ambiguous capped Delete = (%d, %v), dropped=%t attempts=%d",
				deleted, err, tr.dropped.Load(), tr.attempts.Load())
		}
		if n, err := healthy.CountVerticesByPrefix(ctx, "retry/vertex/"); err != nil || n != 2 {
			t.Fatalf("remaining vertices = (%d, %v), want exactly 2", n, err)
		}
	})

	t.Run("failover does not rotate capped edge prefix Delete", func(t *testing.T) {
		for _, head := range []string{"a", "b", "c"} {
			if outcome, err := healthy.PutEdge(ctx, "retry/edges", head, 1, time.Hour); err != nil ||
				outcome != client.PutOutcomeAppliedAndLive {
				t.Fatalf("seed PutEdge(%q) = (%s, %v)", head, outcome, err)
			}
		}
		secondary := newConnectTestServer(t, svc, nil, validation.ConnectInterceptor())
		tr := &commitLossTransport{inner: h2cClient().Transport, pathSuffix: "/DeleteEdgesByPrefix"}
		failover, err := client.NewLanternFailover([]string{primary.url, secondary.url},
			client.WithHTTPClient(&http.Client{Transport: tr}), policy)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = failover.Close() })
		deleted, err := failover.DeleteEdgesByPrefix(ctx,
			client.WithEdgeDeleteTailPrefix("retry/edges"), client.WithEdgeDeleteLimit(1))
		if !errors.Is(err, client.ErrUnavailable) || deleted != 0 || !tr.dropped.Load() || tr.attempts.Load() != 1 {
			t.Fatalf("ambiguous failover Delete = (%d, %v), dropped=%t attempts=%d",
				deleted, err, tr.dropped.Load(), tr.attempts.Load())
		}
		edges, _, err := healthy.ScanEdges(ctx, client.WithEdgeScanTailPrefix("retry/edges"))
		if err != nil || len(edges) != 2 {
			t.Fatalf("remaining edges = (%d, %v), want exactly 2", len(edges), err)
		}
	})
}
