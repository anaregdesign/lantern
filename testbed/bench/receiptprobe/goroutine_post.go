package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var goroutinePeerMetric = regexp.MustCompile(`^lantern_peer_connected\{peer=("(?:[^"\\]|\\.)*")\}$`)

// Only existing passive gauges are retained. Labels come from declared fixture
// peer origins; arbitrary metrics, tokens and the remainder of a scrape are not
// serialized by this candidate collector.
func parseGoroutinePassive(data []byte) (goroutinePassive, error) {
	p := goroutinePassive{Peers: map[string]int64{}}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		selected := name == "lantern_subscribe_active_streams" || name == "lantern_changes_active_streams" || name == "lantern_anti_entropy_cycles_total" || strings.HasPrefix(name, "lantern_peer_connected")
		if !selected {
			continue
		}
		if len(fields) != 2 {
			return p, errors.New("passive metric syntax differs")
		}
		value, err := parseNonnegativeMetricValue(fields[1])
		if err != nil {
			return p, errors.New("passive metric invalid")
		}
		if strings.HasPrefix(name, "lantern_peer_connected") {
			match := goroutinePeerMetric.FindStringSubmatch(name)
			if len(match) != 2 || value > 1 {
				return p, errors.New("peer gauge invalid")
			}
			peer, err := strconv.Unquote(match[1])
			if err != nil || peer == "" || len(peer) > 512 || strings.ContainsAny(peer, "\r\n\x00") {
				return p, errors.New("peer label invalid")
			}
			if _, ok := p.Peers[peer]; ok {
				return p, errors.New("duplicate peer gauge")
			}
			p.Peers[peer] = value
			if len(p.Peers) > 2 {
				return p, errors.New("extra peer gauge")
			}
			continue
		}
		if seen[name] {
			return p, errors.New("duplicate passive metric")
		}
		seen[name] = true
		switch name {
		case "lantern_subscribe_active_streams":
			p.SubscribeActive = value
		case "lantern_changes_active_streams":
			p.ChangesActive = value
		case "lantern_anti_entropy_cycles_total":
			p.AntiEntropyCycles = value
		}
	}
	if scanner.Err() != nil || len(seen) != 3 || len(p.Peers) != 2 {
		return p, errors.New("passive metric coverage incomplete")
	}
	return p, nil
}

func scrapeGoroutinePost(ctx context.Context, client *http.Client, endpoint metricsEndpoint) (goroutinePostReplica, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
	if err != nil {
		return goroutinePostReplica{}, errors.New("metrics request invalid")
	}
	response, err := client.Do(request)
	if err != nil {
		return goroutinePostReplica{}, errors.New("metrics transport failed")
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(data) > 16<<20 {
		return goroutinePostReplica{}, errors.New("metrics response incomplete")
	}
	runtime, err := parseRuntimeMetrics(bytes.NewReader(data))
	if err != nil {
		return goroutinePostReplica{}, errors.New("runtime metrics invalid")
	}
	passive, err := parseGoroutinePassive(data)
	if err != nil {
		return goroutinePostReplica{}, err
	}
	runtime.Endpoint = endpoint.endpoint
	return goroutinePostReplica{Runtime: runtime, Passive: passive}, nil
}

func goroutineMetricsClient(transport *http.Transport) *http.Client {
	return &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// One flight owns exactly three concurrent requests, one shared deadline and
// their join. The outer host-monotonic wrapper counts executable launch/input
// validation/metadata costs too; this duration alone never certifies the window.
func collectGoroutinePost(ctx context.Context, c goroutinePostControl, endpoints []metricsEndpoint, now func() time.Time, scrape func(context.Context, metricsEndpoint) (goroutinePostReplica, error)) goroutinePostCapture {
	start := now()
	out := goroutinePostCapture{Schema: 1, Protocol: goroutineCandidateProtocol, CandidateOnly: true, OffsetSeconds: c.OffsetSeconds, Profile: c.Profile, StartedUTC: start.UTC().Format(time.RFC3339Nano), Verdict: "incomplete"}
	if c.Schema != 1 || c.Protocol != goroutineCandidateProtocol || (c.OffsetSeconds != 5 && c.OffsetSeconds != 10) || len(endpoints) != 3 {
		out.Failure = "invalid candidate flight"
		return out
	}
	if ctx.Err() != nil {
		out.Failure = "post flight cancelled before dispatch"
		return out
	}
	flight, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	values := make([]goroutinePostReplica, 3)
	failures := make([]error, 3)
	var wg sync.WaitGroup
	for i, endpoint := range endpoints {
		wg.Add(1)
		go func(i int, e metricsEndpoint) { defer wg.Done(); values[i], failures[i] = scrape(flight, e) }(i, endpoint)
	}
	wg.Wait()
	out.DurationNS = now().Sub(start).Nanoseconds()
	for _, err := range failures {
		if err != nil {
			out.Failure = "post metrics flight incomplete"
			return out
		}
	}
	if flight.Err() != nil || out.DurationNS < 0 || out.DurationNS > int64(3*time.Second) {
		out.Failure = "post metrics flight late or cancelled"
		return out
	}
	out.Replicas = values
	out.Verdict = "captured"
	return out
}

func runGoroutinePostCollection(args []string) int {
	fs := flag.NewFlagSet("collect-goroutine-post", flag.ContinueOnError)
	input := fs.String("input", "", "candidate flight control")
	outPath := fs.String("out", "", "one post-flight artifact")
	if fs.Parse(args) != nil || *input == "" || *outPath == "" {
		return 2
	}
	caseRoot, err := goroutineCLIPaths(*input, *outPath)
	if err != nil {
		return 2
	}
	data, err := readRetainedFile(*input, 256<<10)
	var c goroutinePostControl
	if err != nil || decodeGoroutineJSON(data, &c) != nil {
		return 2
	}
	fail := func(reason string) int {
		result := goroutinePostCapture{Schema: 1, Protocol: goroutineCandidateProtocol, CandidateOnly: true, OffsetSeconds: c.OffsetSeconds, Profile: c.Profile, Verdict: "incomplete", Failure: reason}
		if writeGoroutineArtifact(*outPath, result) != nil {
			fmt.Fprintln(os.Stderr, "candidate incomplete artifact write failed")
		}
		return 1
	}
	var profile goroutineProfile
	if !goroutineCaseReference(caseRoot, c.Profile) {
		return fail("collector profile outside current case root")
	}
	if goroutineDecode(c.Profile, &profile) != nil || validateGoroutineProfile(profile, false) != nil || goroutineObserverMatches(profile) != nil {
		return fail("normal profile or observer binding differs")
	}
	endpoints, err := parseRuntimeMetricsEndpoints(strings.Join(c.MetricsURLs, ","))
	if err != nil {
		return fail("metrics endpoints invalid")
	}
	for i, e := range endpoints {
		if e.endpoint != profile.Nodes[i].Endpoint {
			return fail("metrics endpoint correspondence differs")
		}
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return fail("metrics transport unavailable")
	}
	transport := base.Clone()
	if !c.OperatorLoopback {
		return fail("declared metrics bridge route required")
	}
	if configureOperatorMetricsLoopback(transport, endpoints) != nil {
		return fail("metrics bridge route invalid")
	}
	observeRuntimeMetricsDial(transport)
	defer transport.CloseIdleConnections()
	client := goroutineMetricsClient(transport)
	result := collectGoroutinePost(context.Background(), c, endpoints, time.Now, func(ctx context.Context, e metricsEndpoint) (goroutinePostReplica, error) {
		return scrapeGoroutinePost(ctx, client, e)
	})
	if err := writeGoroutineArtifact(*outPath, result); err != nil {
		fmt.Fprintln(os.Stderr, "candidate post artifact write failed")
		return 1
	}
	if result.Verdict != "captured" {
		return 1
	}
	return 0
}
