package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoroutinePostRefusesRedirect(t *testing.T) {
	var redirected, initial atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		_, _ = io.WriteString(w, "not a permitted metrics endpoint")
	}))
	defer target.Close()
	endpoints := make([]metricsEndpoint, 0, 3)
	for i := 0; i < 3; i++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			initial.Add(1)
			http.Redirect(w, r, target.URL+"/metrics", http.StatusFound)
		}))
		defer server.Close()
		endpoints = append(endpoints, metricsEndpoint{url: server.URL + "/metrics", endpoint: server.Listener.Addr().String()})
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := goroutineMetricsClient(transport)
	result := collectGoroutinePost(context.Background(), goroutinePostControl{Schema: 1, Protocol: goroutineCandidateProtocol, OffsetSeconds: 5}, endpoints, time.Now, func(ctx context.Context, e metricsEndpoint) (goroutinePostReplica, error) {
		return scrapeGoroutinePost(ctx, client, e)
	})
	if result.Verdict != "incomplete" || initial.Load() != 3 || redirected.Load() != 0 {
		t.Fatal("redirect/fallback escaped fixed one-request endpoints", result.Verdict, initial.Load(), redirected.Load())
	}
}

func TestGoroutinePassiveMetrics(t *testing.T) {
	p, err := parseGoroutinePassive([]byte(goroutineTestPassive()))
	if err != nil || p.SubscribeActive != 2 || len(p.Peers) != 2 {
		t.Fatal(p, err)
	}
	for _, bad := range []string{
		strings.Replace(goroutineTestPassive(), "lantern_changes_active_streams 0\n", "", 1),
		goroutineTestPassive() + "lantern_changes_active_streams 0\n",
		goroutineTestPassive() + "lantern_peer_connected{peer=\"extra\"} 1\n",
		strings.Replace(goroutineTestPassive(), "} 1", "} 0.1", 1),
		strings.Replace(goroutineTestPassive(), "{peer=", "{other=", 1),
		strings.Replace(goroutineTestPassive(), "streams 2", "streams NaN", 1),
	} {
		if _, err := parseGoroutinePassive([]byte(bad)); err == nil {
			t.Fatal("missing/duplicate/foreign passive sample accepted")
		}
	}
	if _, err := parseGoroutinePassive([]byte(goroutineTestPassive() + "arbitrary_private_metric 123\n")); err != nil {
		t.Fatal("unselected metric affected fixed projection")
	}
}

func TestGoroutinePostFlight(t *testing.T) {
	endpoints := []metricsEndpoint{{endpoint: "a"}, {endpoint: "b"}, {endpoint: "c"}}
	for _, mode := range []string{"captured", "cancel_before", "cancel_during", "failure", "late"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel_before" {
				cancel()
			}
			var mu sync.Mutex
			calls := map[string]int{}
			joined := 0
			entered := make(chan struct{}, 3)
			release := make(chan struct{})
			monitorDone := make(chan struct{})
			if mode == "cancel_during" {
				go func() {
					defer close(monitorDone)
					for i := 0; i < 3; i++ {
						<-entered
					}
					cancel()
					close(release)
				}()
			} else {
				close(release)
				close(monitorDone)
			}
			clockCalls := 0
			now := func() time.Time {
				clockCalls++
				if mode == "late" && clockCalls > 1 {
					return time.Unix(100, 0).Add(4 * time.Second)
				}
				return time.Unix(100, 0)
			}
			out := collectGoroutinePost(ctx, goroutinePostControl{Schema: 1, Protocol: goroutineCandidateProtocol, OffsetSeconds: 5}, endpoints, now, func(ctx context.Context, e metricsEndpoint) (goroutinePostReplica, error) {
				mu.Lock()
				calls[e.endpoint]++
				mu.Unlock()
				defer func() { mu.Lock(); joined++; mu.Unlock() }()
				if mode == "cancel_during" {
					entered <- struct{}{}
					<-release
					return goroutinePostReplica{}, ctx.Err()
				}
				if mode == "failure" {
					return goroutinePostReplica{}, errors.New("fixed test failure")
				}
				return goroutinePostReplica{Runtime: runtimeMetrics{Endpoint: e.endpoint, Goroutines: 50}}, nil
			})
			<-monitorDone
			if (out.Verdict == "captured") != (mode == "captured") {
				t.Fatal(mode, out.Verdict, out.Failure)
			}
			mu.Lock()
			defer mu.Unlock()
			if mode == "cancel_before" {
				if len(calls) != 0 {
					t.Fatal("cancelled flight dispatched")
				}
			} else {
				if joined != 3 || len(calls) != 3 {
					t.Fatal("flight did not join all owned requests")
				}
				for _, count := range calls {
					if count != 1 {
						t.Fatal("flight retried")
					}
				}
			}
			if !out.CandidateOnly || out.Qualification || out.ReleaseAdmission {
				t.Fatal("flight claimed acceptance")
			}
		})
	}
}

type goroutineRoundTripper func(*http.Request) (*http.Response, error)

func (f goroutineRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type goroutineResponseBody struct {
	io.Reader
	closed   bool
	closeErr error
}

func (b *goroutineResponseBody) Close() error { b.closed = true; return b.closeErr }

func TestGoroutinePostSingleScrapeProjection(t *testing.T) {
	runtime := "go_goroutines 50\ngo_memstats_heap_alloc_bytes 100\ngo_gc_duration_seconds_count 1\ngo_memstats_next_gc_bytes 200\nlantern_receipt_entries 1\nlantern_receipt_bytes 100\nlantern_mutation_log_capacity 10000\nlantern_mutation_log_evicted_total 0\ngo_memstats_last_gc_time_seconds 1\nlantern_receipt_oldest_deadline_seconds 2\nlantern_mutation_log_fill_ratio 0.1\n"
	for _, failClose := range []bool{false, true} {
		body := &goroutineResponseBody{Reader: strings.NewReader(runtime + goroutineTestPassive())}
		if failClose {
			body.closeErr = errors.New("fixed close error")
		}
		calls := 0
		client := &http.Client{Transport: goroutineRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Body: body}, nil
		})}
		value, err := scrapeGoroutinePost(context.Background(), client, metricsEndpoint{url: "http://localhost:9390/metrics", endpoint: "localhost:9390"})
		if (err != nil) != failClose || !body.closed || calls != 1 {
			t.Fatal("scrape close/one-flight contract", err, calls)
		}
		if !failClose && (value.Runtime.Goroutines != 50 || value.Passive.SubscribeActive != 2) {
			t.Fatal("same-scrape projection differs")
		}
	}
}
