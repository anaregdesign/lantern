// Command receiptprobe benchmarks receipt-bearing Edge Delete admission followed
// by an immediate same-operation status lookup over Connect/h2c.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	graphv1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

const (
	authorizationHeader = "Authorization"
	bearerPrefix        = "Bearer "
)

type receiptClient interface {
	DeleteEdge(context.Context, *connect.Request[graphv1.DeleteEdgeRequest]) (*connect.Response[graphv1.DeleteEdgeResponse], error)
	GetReceiptStatus(context.Context, *connect.Request[graphv1.GetReceiptStatusRequest]) (*connect.Response[graphv1.GetReceiptStatusResponse], error)
}

type receiptEndpoint struct {
	address    string
	client     receiptClient
	capability *graphv1.GetReceiptCapabilityResponse
	issuedAt   time.Time
}

type receiptOperation struct {
	deleteRequest     *graphv1.DeleteEdgeRequest
	operationID       []byte
	logicalCallID     []byte
	intentSHA256      [sha256.Size]byte
	deadlineUnixMS    uint64
	expectedItem      uint32
	expectedItemCount uint32
}

type rpcSample struct {
	latency time.Duration
	status  string
	reason  string
}

type sampleCollector struct {
	mu      sync.Mutex
	samples []rpcSample
}

func (c *sampleCollector) add(sample rpcSample) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.samples = append(c.samples, sample)
}

func (c *sampleCollector) snapshot() []rpcSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]rpcSample(nil), c.samples...)
}

type probeConfig struct {
	phase          string
	token          string
	duration       time.Duration
	requestTimeout time.Duration
	concurrency    int
	pairRPS        int
	steadyMetrics  *steadyMetricsConfig
}

type summary struct {
	Count                  int64            `json:"count"`
	Total                  time.Duration    `json:"total"`
	Average                time.Duration    `json:"average"`
	Fastest                time.Duration    `json:"fastest"`
	Slowest                time.Duration    `json:"slowest"`
	RPS                    float64          `json:"rps"`
	StatusCodeDistribution map[string]int64 `json:"statusCodeDistribution"`
	LatencyDistribution    []latencyPoint   `json:"latencyDistribution"`
}

type latencyPoint struct {
	Percentage int           `json:"percentage"`
	Latency    time.Duration `json:"latency"`
}

type resultSet struct {
	admission summary
	lookup    summary
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "evaluate-leak" {
		os.Exit(runReceiptLeakEvaluation(os.Args[2:]))
	}
	var (
		endpointsFlag   = flag.String("endpoints", "", "comma-separated Lantern endpoint URLs")
		token           = flag.String("token", "", "bearer token")
		phase           = flag.String("phase", "", "benchmark phase name")
		duration        = flag.Duration("duration", 0, "offered-load duration")
		requestTimeout  = flag.Duration("request-timeout", 5*time.Second, "per-RPC timeout")
		concurrency     = flag.Int("concurrency", 0, "maximum concurrent operation pairs")
		pairRPS         = flag.Int("pair-rps", 0, "offered receipt operation pairs per second")
		admissionReport = flag.String("admission-report", "", "admission summary output path")
		lookupReport    = flag.String("lookup-report", "", "lookup summary output path")
		metricsURLs     = flag.String("metrics-endpoints", "", "comma-separated replica /metrics URLs during steady load")
		metricsInterval = flag.Duration("metrics-interval", 0, "steady replica sampling interval")
		metricsReport   = flag.String("metrics-report", "", "steady replica samples output path")
	)
	flag.Parse()

	endpointAddresses, err := parseEndpoints(*endpointsFlag)
	if err != nil {
		fatalf("%v", err)
	}
	if *token == "" {
		fatalf("-token is required")
	}
	if *phase == "" {
		fatalf("-phase is required")
	}
	if *duration <= 0 {
		fatalf("-duration must be positive")
	}
	if *requestTimeout <= 0 {
		fatalf("-request-timeout must be positive")
	}
	if *concurrency <= 0 {
		fatalf("-concurrency must be positive")
	}
	if *pairRPS <= 0 {
		fatalf("-pair-rps must be positive")
	}
	if (*admissionReport == "") != (*lookupReport == "") {
		fatalf("-admission-report and -lookup-report must be supplied together")
	}
	var steadyMetrics *steadyMetricsConfig
	if *metricsURLs != "" || *metricsInterval != 0 || *metricsReport != "" {
		if *phase != "steady" || *metricsReport == "" || *metricsInterval <= 0 ||
			*metricsInterval > *duration {
			fatalf("steady metrics require -phase steady, -metrics-report, and an interval within the offered duration")
		}
		replicas, err := parseRuntimeMetricsEndpoints(*metricsURLs)
		if err != nil {
			fatalf("steady metrics endpoints: %v", err)
		}
		steadyMetrics = &steadyMetricsConfig{endpoints: replicas, interval: *metricsInterval}
	}

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	httpClient := &http.Client{Transport: &http.Transport{Protocols: protocols}}

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	endpoints, err := discoverEndpoints(setupCtx, httpClient, endpointAddresses, *token)
	setupCancel()
	if err != nil {
		fatalf("discover receipt endpoints: %v", err)
	}

	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		fatalf("generate run nonce: %v", err)
	}
	nonce[0] |= 1

	cfg := probeConfig{
		phase:          *phase,
		token:          *token,
		duration:       *duration,
		requestTimeout: *requestTimeout,
		concurrency:    *concurrency,
		pairRPS:        *pairRPS,
		steadyMetrics:  steadyMetrics,
	}
	runCtx, runCancel := context.WithTimeout(
		context.Background(),
		*duration+2**requestTimeout+30*time.Second,
	)
	results, steadyReport, runErr := runProbe(runCtx, cfg, endpoints, nonce)
	runCancel()

	if *admissionReport != "" {
		if err := writeSummary(*admissionReport, results.admission); err != nil {
			fatalf("write admission report: %v", err)
		}
		if err := writeSummary(*lookupReport, results.lookup); err != nil {
			fatalf("write lookup report: %v", err)
		}
	}
	if steadyMetrics != nil {
		if steadyReport == nil {
			fatalf("steady metrics report was not captured")
		}
		if err := writeReceiptArtifact(*metricsReport, steadyReport); err != nil {
			fatalf("write steady metrics report: %v", err)
		}
	}
	if runErr != nil {
		fatalf("%v", runErr)
	}

	fmt.Printf(
		"receipt probe %s passed: admission=%d lookup=%d\n",
		*phase,
		results.admission.Count,
		results.lookup.Count,
	)
}

func parseEndpoints(raw string) ([]string, error) {
	var endpoints []string
	for _, value := range strings.Split(raw, ",") {
		endpoint := strings.TrimSpace(value)
		if endpoint == "" {
			continue
		}
		if !strings.HasPrefix(endpoint, "http://") {
			return nil, fmt.Errorf("endpoint %q must use http:// for h2c", endpoint)
		}
		endpoints = append(endpoints, strings.TrimRight(endpoint, "/"))
	}
	if len(endpoints) == 0 {
		return nil, errors.New("-endpoints must contain at least one URL")
	}
	return endpoints, nil
}

func discoverEndpoints(
	ctx context.Context,
	httpClient *http.Client,
	addresses []string,
	token string,
) ([]receiptEndpoint, error) {
	endpoints := make([]receiptEndpoint, 0, len(addresses))
	for _, address := range addresses {
		client := graphv1connect.NewLanternServiceClient(httpClient, address)
		request := authorizedRequest(&graphv1.GetReceiptCapabilityRequest{}, token)
		response, err := client.GetReceiptCapability(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("%s capability: %w", address, err)
		}
		capability := response.Msg
		if !capability.GetEnabled() {
			return nil, fmt.Errorf("%s capability is disabled", address)
		}
		policy := capability.GetPolicy()
		endpoint := capability.GetEndpoint()
		if policy == nil || endpoint == nil {
			return nil, fmt.Errorf("%s capability omitted policy or endpoint", address)
		}
		if len(policy.GetDeploymentEpoch()) != len(mutationreceipt.Epoch{}) {
			return nil, fmt.Errorf(
				"%s policy epoch length = %d, want %d",
				address,
				len(policy.GetDeploymentEpoch()),
				len(mutationreceipt.Epoch{}),
			)
		}
		if len(endpoint.GetNodeId()) != len(mutationreceipt.Epoch{}) {
			return nil, fmt.Errorf(
				"%s endpoint node ID length = %d, want %d",
				address,
				len(endpoint.GetNodeId()),
				len(mutationreceipt.Epoch{}),
			)
		}
		if len(endpoint.GetGeneration()) == 0 {
			return nil, fmt.Errorf("%s endpoint generation is empty", address)
		}
		if policy.GetRetentionMs() == 0 {
			return nil, fmt.Errorf("%s receipt retention is zero", address)
		}
		if len(policy.GetFingerprint()) != sha256.Size ||
			policy.GetMaxEntries() == 0 ||
			policy.GetMaxBytes() == 0 {
			return nil, fmt.Errorf("%s receipt policy is incomplete", address)
		}
		if capability.GetServerNowUnixMs() > math.MaxInt64 {
			return nil, fmt.Errorf("%s server time overflows int64", address)
		}
		endpoints = append(endpoints, receiptEndpoint{
			address:    address,
			client:     client,
			capability: capability,
			issuedAt:   time.UnixMilli(int64(capability.GetServerNowUnixMs())).Add(-time.Second),
		})
	}
	if err := validateEndpointSet(endpoints); err != nil {
		return nil, err
	}
	return endpoints, nil
}

func validateEndpointSet(endpoints []receiptEndpoint) error {
	if len(endpoints) == 0 {
		return errors.New("no receipt endpoints discovered")
	}
	firstPolicy := endpoints[0].capability.GetPolicy()
	seenNodeIDs := make(map[string]string, len(endpoints))
	for _, endpoint := range endpoints {
		policy := endpoint.capability.GetPolicy()
		if !bytes.Equal(policy.GetDeploymentEpoch(), firstPolicy.GetDeploymentEpoch()) ||
			!bytes.Equal(policy.GetFingerprint(), firstPolicy.GetFingerprint()) ||
			policy.GetRetentionMs() != firstPolicy.GetRetentionMs() ||
			policy.GetMaxEntries() != firstPolicy.GetMaxEntries() ||
			policy.GetMaxBytes() != firstPolicy.GetMaxBytes() {
			return fmt.Errorf("%s receipt policy differs from the first endpoint", endpoint.address)
		}
		nodeID := string(endpoint.capability.GetEndpoint().GetNodeId())
		if previous, ok := seenNodeIDs[nodeID]; ok {
			return fmt.Errorf("%s and %s report the same receipt node ID", previous, endpoint.address)
		}
		seenNodeIDs[nodeID] = endpoint.address
	}
	return nil
}

func runProbe(
	ctx context.Context,
	cfg probeConfig,
	endpoints []receiptEndpoint,
	nonce [16]byte,
) (resultSet, *steadyMetricsReport, error) {
	if len(endpoints) == 0 {
		return resultSet{}, nil, errors.New("no receipt endpoints configured")
	}

	var admissionSamples, lookupSamples sampleCollector
	jobs := make(chan uint64, cfg.concurrency)
	var workers sync.WaitGroup
	for range cfg.concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for sequence := range jobs {
				endpoint := &endpoints[(sequence-1)%uint64(len(endpoints))]
				operation, err := newReceiptOperation(endpoint, nonce, sequence, cfg.phase)
				if err != nil {
					admissionSamples.add(rpcSample{status: "DataLoss"})
					continue
				}
				admission, lookup, lookedUp := executeOperation(
					ctx,
					cfg.token,
					cfg.requestTimeout,
					endpoint.client,
					operation,
				)
				admissionSamples.add(admission)
				if lookedUp {
					lookupSamples.add(lookup)
				}
			}
		}()
	}

	start := time.Now()
	var metricsStop chan struct{}
	var metricsDone chan steadyMetricsReport
	if cfg.steadyMetrics != nil {
		metricsStop = make(chan struct{})
		metricsDone = make(chan steadyMetricsReport, 1)
		go func() {
			metricsDone <- sampleSteadyMetrics(ctx, *cfg.steadyMetrics, start, metricsStop)
		}()
	}
	ticker := time.NewTicker(time.Second / time.Duration(cfg.pairRPS))
	timer := time.NewTimer(cfg.duration)
	var sequence uint64
schedule:
	for {
		select {
		case <-ctx.Done():
			break schedule
		case <-timer.C:
			break schedule
		case <-ticker.C:
			sequence++
			select {
			case jobs <- sequence:
			case <-ctx.Done():
				break schedule
			case <-timer.C:
				break schedule
			}
		}
	}
	ticker.Stop()
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	close(jobs)
	workers.Wait()
	elapsed := time.Since(start)
	var steadyReport *steadyMetricsReport
	if metricsStop != nil {
		close(metricsStop)
		report := <-metricsDone
		report.DurationMS = elapsed.Milliseconds()
		if err := validateSteadyMetrics(report, *cfg.steadyMetrics, cfg.duration); err != nil {
			report.Failure = err.Error()
		}
		steadyReport = &report
	}

	results := resultSet{
		admission: summarize(admissionSamples.snapshot(), elapsed),
		lookup:    summarize(lookupSamples.snapshot(), elapsed),
	}
	if steadyReport != nil && steadyReport.Failure != "" {
		return results, steadyReport, fmt.Errorf("steady runtime sampling: %s", steadyReport.Failure)
	}
	if ctx.Err() != nil {
		return results, steadyReport, fmt.Errorf("receipt probe context ended: %w", ctx.Err())
	}
	if results.admission.Count == 0 {
		return results, steadyReport, errors.New("receipt probe admitted no operations")
	}
	if results.lookup.Count != results.admission.Count {
		return results, steadyReport, fmt.Errorf(
			"receipt lookup coverage mismatch: admission=%d lookup=%d",
			results.admission.Count,
			results.lookup.Count,
		)
	}
	if nonOKCount(results.admission) != 0 || nonOKCount(results.lookup) != 0 {
		return results, steadyReport, fmt.Errorf(
			"receipt probe observed non-OK results: admission=%d (%s) lookup=%d (%s)",
			nonOKCount(results.admission),
			firstFailure(admissionSamples.snapshot()),
			nonOKCount(results.lookup),
			firstFailure(lookupSamples.snapshot()),
		)
	}
	return results, steadyReport, nil
}

func newReceiptOperation(
	endpoint *receiptEndpoint,
	nonce [16]byte,
	sequence uint64,
	phase string,
) (receiptOperation, error) {
	var epoch mutationreceipt.Epoch
	copy(epoch[:], endpoint.capability.GetPolicy().GetDeploymentEpoch())

	var randomness [24]byte
	copy(randomness[:16], nonce[:])
	binary.BigEndian.PutUint64(randomness[16:], sequence)
	id, err := mutationreceipt.NewID(epoch, endpoint.issuedAt, randomness)
	if err != nil {
		return receiptOperation{}, fmt.Errorf("construct operation ID: %w", err)
	}

	logicalCallID := make([]byte, len(mutationreceipt.GroupID{}))
	copy(logicalCallID[:8], nonce[:8])
	binary.BigEndian.PutUint64(logicalCallID[8:], sequence)

	tailKey := fmt.Sprintf("bench:receipt:%s:%d", phase, sequence)
	headKey := "bench:receipt:missing"
	intentSHA256 := edgeDeleteIntentSHA256(tailKey, headKey)
	deadlineUnixMS := uint64(
		endpoint.issuedAt.Add(
			time.Duration(endpoint.capability.GetPolicy().GetRetentionMs()) * time.Millisecond,
		).UnixMilli(),
	)

	return receiptOperation{
		deleteRequest: &graphv1.DeleteEdgeRequest{
			Tail: tailKey,
			Head: headKey,
			ReceiptContext: &graphv1.MutationReceiptContext{
				OperationIds:  [][]byte{id.Bytes()},
				LogicalCallId: append([]byte(nil), logicalCallID...),
				Endpoint: &graphv1.ReceiptEndpoint{
					NodeId:     append([]byte(nil), endpoint.capability.GetEndpoint().GetNodeId()...),
					Generation: append([]byte(nil), endpoint.capability.GetEndpoint().GetGeneration()...),
				},
			},
		},
		operationID:       id.Bytes(),
		logicalCallID:     logicalCallID,
		intentSHA256:      intentSHA256,
		deadlineUnixMS:    deadlineUnixMS,
		expectedItem:      0,
		expectedItemCount: 1,
	}, nil
}

func executeOperation(
	parent context.Context,
	token string,
	requestTimeout time.Duration,
	client receiptClient,
	operation receiptOperation,
) (rpcSample, rpcSample, bool) {
	requestCtx, cancel := context.WithTimeout(parent, requestTimeout)
	start := time.Now()
	response, err := client.DeleteEdge(
		requestCtx,
		authorizedRequest(operation.deleteRequest, token),
	)
	admission := sampleFor(time.Since(start), err)
	cancel()
	if err != nil {
		return admission, rpcSample{}, false
	}
	if response == nil || response.Msg == nil || response.Msg.GetExisted() {
		return rpcSample{
			latency: admission.latency,
			status:  "DataLoss",
			reason:  "DeleteEdge returned an invalid semantic result",
		}, rpcSample{}, false
	}

	requestCtx, cancel = context.WithTimeout(parent, requestTimeout)
	start = time.Now()
	statusResponse, err := client.GetReceiptStatus(
		requestCtx,
		authorizedRequest(&graphv1.GetReceiptStatusRequest{
			OperationId: operation.operationID,
		}, token),
	)
	lookup := sampleFor(time.Since(start), err)
	cancel()
	if err != nil {
		return admission, lookup, true
	}
	if err := verifyConfirmedStatus(statusResponse, operation); err != nil {
		lookup.status = "DataLoss"
		lookup.reason = err.Error()
	}
	return admission, lookup, true
}

func verifyConfirmedStatus(
	response *connect.Response[graphv1.GetReceiptStatusResponse],
	operation receiptOperation,
) error {
	if response == nil || response.Msg == nil || response.Msg.GetStatus() == nil {
		return errors.New("lookup returned no status")
	}
	status := response.Msg.GetStatus()
	if !bytes.Equal(status.GetOperationId(), operation.operationID) {
		return errors.New("lookup operation ID mismatch")
	}
	if status.GetState() != graphv1.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED {
		return fmt.Errorf("lookup state = %s, want CONFIRMED", status.GetState())
	}
	receipt := status.GetReceipt()
	if receipt == nil {
		return errors.New("confirmed lookup returned no receipt")
	}
	if !bytes.Equal(receipt.GetOperationId(), operation.operationID) ||
		!bytes.Equal(receipt.GetLogicalCallId(), operation.logicalCallID) {
		return errors.New("confirmed receipt identity mismatch")
	}
	if !bytes.Equal(receipt.GetIntentSha256(), operation.intentSHA256[:]) {
		return errors.New("confirmed receipt intent mismatch")
	}
	if receipt.GetDeadlineUnixMs() != operation.deadlineUnixMS {
		return fmt.Errorf(
			"confirmed receipt deadline = %d, want %d",
			receipt.GetDeadlineUnixMs(),
			operation.deadlineUnixMS,
		)
	}
	if receipt.GetItemIndex() != operation.expectedItem ||
		receipt.GetItemCount() != operation.expectedItemCount {
		return errors.New("confirmed receipt item coordinates mismatch")
	}
	result, ok := receipt.GetOriginalResult().GetResult().(*graphv1.ReceiptResult_DeleteEdgeExisted)
	if !ok || result.DeleteEdgeExisted {
		return errors.New("confirmed receipt original result is not delete_edge_existed=false")
	}
	return nil
}

func edgeDeleteIntentSHA256(tailKey, headKey string) [sha256.Size]byte {
	tailBytes := []byte(tailKey)
	headBytes := []byte(headKey)
	canonical := make([]byte, 0, 1+8+len(tailKey)+8+len(headKey))
	canonical = append(canonical, byte(mutationreceipt.DeleteEdge))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(tailBytes)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, tailBytes...)
	binary.BigEndian.PutUint64(length[:], uint64(len(headBytes)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, headBytes...)
	return mutationreceipt.IntentDigest(canonical)
}

func authorizedRequest[T any](message *T, token string) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set(authorizationHeader, bearerPrefix+token)
	return request
}

func sampleFor(latency time.Duration, err error) rpcSample {
	if err == nil {
		return rpcSample{latency: latency, status: "OK"}
	}
	return rpcSample{latency: latency, status: connectStatusName(connect.CodeOf(err))}
}

func connectStatusName(code connect.Code) string {
	switch code {
	case connect.CodeCanceled:
		return "Canceled"
	case connect.CodeUnknown:
		return "Unknown"
	case connect.CodeInvalidArgument:
		return "InvalidArgument"
	case connect.CodeDeadlineExceeded:
		return "DeadlineExceeded"
	case connect.CodeNotFound:
		return "NotFound"
	case connect.CodeAlreadyExists:
		return "AlreadyExists"
	case connect.CodePermissionDenied:
		return "PermissionDenied"
	case connect.CodeResourceExhausted:
		return "ResourceExhausted"
	case connect.CodeFailedPrecondition:
		return "FailedPrecondition"
	case connect.CodeAborted:
		return "Aborted"
	case connect.CodeOutOfRange:
		return "OutOfRange"
	case connect.CodeUnimplemented:
		return "Unimplemented"
	case connect.CodeInternal:
		return "Internal"
	case connect.CodeUnavailable:
		return "Unavailable"
	case connect.CodeDataLoss:
		return "DataLoss"
	case connect.CodeUnauthenticated:
		return "Unauthenticated"
	default:
		return "Unknown"
	}
}

func summarize(samples []rpcSample, elapsed time.Duration) summary {
	result := summary{
		Count:                  int64(len(samples)),
		Total:                  elapsed,
		StatusCodeDistribution: make(map[string]int64),
	}
	if len(samples) == 0 {
		return result
	}

	latencies := make([]time.Duration, 0, len(samples))
	var totalLatency time.Duration
	for _, sample := range samples {
		latencies = append(latencies, sample.latency)
		totalLatency += sample.latency
		result.StatusCodeDistribution[sample.status]++
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	result.Average = totalLatency / time.Duration(len(latencies))
	result.Fastest = latencies[0]
	result.Slowest = latencies[len(latencies)-1]
	if elapsed > 0 {
		result.RPS = float64(len(samples)) / elapsed.Seconds()
	}
	for _, percentile := range []int{50, 75, 90, 95, 99} {
		index := int(math.Ceil(float64(percentile)*float64(len(latencies))/100)) - 1
		if index < 0 {
			index = 0
		}
		result.LatencyDistribution = append(result.LatencyDistribution, latencyPoint{
			Percentage: percentile,
			Latency:    latencies[index],
		})
	}
	return result
}

func nonOKCount(value summary) int64 {
	var nonOK int64
	for code, count := range value.StatusCodeDistribution {
		if code != "OK" {
			nonOK += count
		}
	}
	return nonOK
}

func firstFailure(samples []rpcSample) string {
	for _, sample := range samples {
		if sample.status == "OK" {
			continue
		}
		if sample.reason != "" {
			return sample.reason
		}
		return sample.status
	}
	return "none"
}

func writeSummary(path string, value summary) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "receiptprobe: "+format+"\n", args...)
	os.Exit(1)
}
