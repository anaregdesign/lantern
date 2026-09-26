// Command receiptprobe benchmarks receipt-bearing mutations followed by an
// immediate same-operation status lookup over Connect/h2c.
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

type receiptFamily string

const (
	receiptVertexPut    receiptFamily = "receipt_vertex_put"
	receiptVertexDelete receiptFamily = "receipt_vertex_delete"
	receiptEdgeDelete   receiptFamily = "receipt_edge_delete"
	receiptEdgeAdd      receiptFamily = "receipt_edge_add"
)

func (family receiptFamily) mutationKind() (graphv1.ReceiptMutationKind, error) {
	switch family {
	case receiptVertexPut:
		return graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_PUT_VERTEX, nil
	case receiptVertexDelete:
		return graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_VERTEX, nil
	case receiptEdgeDelete:
		return graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_EDGE, nil
	case receiptEdgeAdd:
		return graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_ADD_EDGE, nil
	default:
		return graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_UNSPECIFIED,
			fmt.Errorf("unsupported receipt family %q", family)
	}
}

type receiptClient interface {
	PutVertex(context.Context, *connect.Request[graphv1.PutVertexRequest]) (*connect.Response[graphv1.PutVertexResponse], error)
	DeleteVertex(context.Context, *connect.Request[graphv1.DeleteVertexRequest]) (*connect.Response[graphv1.DeleteVertexResponse], error)
	DeleteEdge(context.Context, *connect.Request[graphv1.DeleteEdgeRequest]) (*connect.Response[graphv1.DeleteEdgeResponse], error)
	AddEdge(context.Context, *connect.Request[graphv1.AddEdgeRequest]) (*connect.Response[graphv1.AddEdgeResponse], error)
	GetReceiptStatus(context.Context, *connect.Request[graphv1.GetReceiptStatusRequest]) (*connect.Response[graphv1.GetReceiptStatusResponse], error)
}

type receiptEndpoint struct {
	address    string
	client     receiptClient
	capability *graphv1.GetReceiptCapabilityResponse
	issuedAt   time.Time
}

type receiptOperation struct {
	family                receiptFamily
	putVertexRequest      *graphv1.PutVertexRequest
	deleteVertexRequest   *graphv1.DeleteVertexRequest
	deleteEdgeRequest     *graphv1.DeleteEdgeRequest
	addEdgeRequest        *graphv1.AddEdgeRequest
	operationID           []byte
	logicalCallID         []byte
	intentSHA256          [sha256.Size]byte
	deadlineUnixMS        uint64
	expectedItem          uint32
	expectedItemCount     uint32
	expectedAddWeightBits uint32
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
	family         receiptFamily
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
		familyFlag      = flag.String("family", "", "receipt family (receipt_vertex_put, receipt_vertex_delete, receipt_edge_delete, receipt_edge_add)")
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

	family := receiptFamily(*familyFlag)
	if _, err := family.mutationKind(); err != nil {
		fatalf("%v", err)
	}
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
	endpoints, err := discoverEndpoints(setupCtx, httpClient, endpointAddresses, *token, family)
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
		family:         family,
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
		"receipt probe %s %s passed: admission=%d lookup=%d\n",
		family,
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
	family receiptFamily,
) ([]receiptEndpoint, error) {
	if _, err := family.mutationKind(); err != nil {
		return nil, err
	}
	endpoints := make([]receiptEndpoint, 0, len(addresses))
	for _, address := range addresses {
		client := graphv1connect.NewLanternServiceClient(httpClient, address)
		request := authorizedRequest(&graphv1.GetReceiptCapabilityRequest{}, token)
		response, err := client.GetReceiptCapability(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("%s capability: %w", address, err)
		}
		if response == nil || response.Msg == nil {
			return nil, fmt.Errorf("%s capability response is missing", address)
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
	if err := validateEndpointSet(endpoints, family); err != nil {
		return nil, err
	}
	return endpoints, nil
}

func validateEndpointSet(endpoints []receiptEndpoint, family receiptFamily) error {
	if len(endpoints) == 0 {
		return errors.New("no receipt endpoints discovered")
	}
	required, err := family.mutationKind()
	if err != nil {
		return err
	}
	if endpoints[0].capability == nil || endpoints[0].capability.GetPolicy() == nil {
		return fmt.Errorf("%s receipt capability omitted policy", endpoints[0].address)
	}
	firstPolicy := endpoints[0].capability.GetPolicy()
	seenNodeIDs := make(map[string]string, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.capability == nil || !endpoint.capability.GetEnabled() ||
			endpoint.capability.GetPolicy() == nil || endpoint.capability.GetEndpoint() == nil {
			return fmt.Errorf("%s receipt capability is incomplete or disabled", endpoint.address)
		}
		supported := false
		for _, kind := range endpoint.capability.GetSupportedMutations() {
			if kind == required {
				supported = true
				break
			}
		}
		if !supported {
			return fmt.Errorf("%s does not support receipt mutation %s", endpoint.address, required)
		}
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
	if _, err := cfg.family.mutationKind(); err != nil {
		return resultSet{}, nil, err
	}

	var admissionSamples, lookupSamples sampleCollector
	jobs := make(chan uint64, cfg.concurrency)
	var workers sync.WaitGroup
	for range cfg.concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for sequence := range jobs {
				slot := sequence - 1
				if cfg.family == receiptEdgeAdd {
					slot /= 2
				}
				endpoint := &endpoints[slot%uint64(len(endpoints))]
				operation, err := newReceiptOperation(endpoint, cfg.family, nonce, sequence, cfg.phase)
				if err != nil {
					admissionSamples.add(rpcSample{status: "DataLoss", reason: err.Error()})
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
	family receiptFamily,
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

	deadlineUnixMS := uint64(
		endpoint.issuedAt.Add(
			time.Duration(endpoint.capability.GetPolicy().GetRetentionMs()) * time.Millisecond,
		).UnixMilli(),
	)

	operation := receiptOperation{
		family:            family,
		operationID:       id.Bytes(),
		logicalCallID:     logicalCallID,
		deadlineUnixMS:    deadlineUnixMS,
		expectedItem:      0,
		expectedItemCount: 1,
	}
	receiptContext := &graphv1.MutationReceiptContext{
		OperationIds:  [][]byte{operation.operationID},
		LogicalCallId: append([]byte(nil), logicalCallID...),
		Endpoint: &graphv1.ReceiptEndpoint{
			NodeId:     append([]byte(nil), endpoint.capability.GetEndpoint().GetNodeId()...),
			Generation: append([]byte(nil), endpoint.capability.GetEndpoint().GetGeneration()...),
		},
	}
	key := fmt.Sprintf("bench:receipt:%x:%s:%d", nonce[:8], phase, sequence)
	switch family {
	case receiptVertexPut:
		vertex := &graphv1.Vertex{
			Key: key, Value: &graphv1.Vertex_String_{String_: "receipt-bench"},
		}
		operation.putVertexRequest = &graphv1.PutVertexRequest{
			Vertex: vertex, IfAbsent: true, ReceiptContext: receiptContext,
		}
		operation.intentSHA256, err = vertexPutIntentSHA256(vertex)
		if err != nil {
			return receiptOperation{}, err
		}
	case receiptVertexDelete:
		operation.deleteVertexRequest = &graphv1.DeleteVertexRequest{
			Key: key, ReceiptContext: receiptContext,
		}
		operation.intentSHA256 = vertexDeleteIntentSHA256(key)
	case receiptEdgeDelete:
		head := "bench:receipt:missing"
		operation.deleteEdgeRequest = &graphv1.DeleteEdgeRequest{
			Tail: key, Head: head, ReceiptContext: receiptContext,
		}
		operation.intentSHA256 = edgeDeleteIntentSHA256(key, head)
	case receiptEdgeAdd:
		edge := &graphv1.Edge{
			Tail: fmt.Sprintf("bench:receipt:%x:%s:%d", nonce[:8], phase, (sequence-1)/2),
			Head: "bench:receipt:add", Weight: math.MaxFloat32,
		}
		contribID := append([]byte(nil), randomness[:]...)
		operation.addEdgeRequest = &graphv1.AddEdgeRequest{
			Edge: edge, ContribId: contribID, ReceiptContext: receiptContext,
		}
		operation.intentSHA256, err = edgeAddIntentSHA256(edge, contribID)
		if err != nil {
			return receiptOperation{}, err
		}
		operation.expectedAddWeightBits = math.Float32bits(edge.Weight)
	default:
		return receiptOperation{}, fmt.Errorf("unsupported receipt family %q", family)
	}
	return operation, nil
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
	var err, semanticErr error
	switch operation.family {
	case receiptVertexPut:
		var response *connect.Response[graphv1.PutVertexResponse]
		response, err = client.PutVertex(requestCtx, authorizedRequest(operation.putVertexRequest, token))
		if err == nil && (response == nil || response.Msg == nil ||
			response.Msg.GetOutcome() != graphv1.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE) {
			semanticErr = errors.New("PutVertex did not return APPLIED_AND_LIVE")
		}
	case receiptVertexDelete:
		var response *connect.Response[graphv1.DeleteVertexResponse]
		response, err = client.DeleteVertex(requestCtx, authorizedRequest(operation.deleteVertexRequest, token))
		if err == nil && (response == nil || response.Msg == nil || response.Msg.GetExisted()) {
			semanticErr = errors.New("DeleteVertex did not return existed=false")
		}
	case receiptEdgeDelete:
		var response *connect.Response[graphv1.DeleteEdgeResponse]
		response, err = client.DeleteEdge(requestCtx, authorizedRequest(operation.deleteEdgeRequest, token))
		if err == nil && (response == nil || response.Msg == nil || response.Msg.GetExisted()) {
			semanticErr = errors.New("DeleteEdge did not return existed=false")
		}
	case receiptEdgeAdd:
		var response *connect.Response[graphv1.AddEdgeResponse]
		response, err = client.AddEdge(requestCtx, authorizedRequest(operation.addEdgeRequest, token))
		if err == nil {
			if response == nil || response.Msg == nil || response.Msg.GetEffectiveWeight() == 0 {
				semanticErr = errors.New("AddEdge returned an absent or zero effective weight")
			} else {
				operation.expectedAddWeightBits = math.Float32bits(response.Msg.GetEffectiveWeight())
			}
		}
	default:
		semanticErr = fmt.Errorf("unsupported receipt family %q", operation.family)
	}
	admission := sampleFor(time.Since(start), err)
	cancel()
	if err != nil {
		return admission, rpcSample{}, false
	}
	if semanticErr != nil {
		admission.status = "DataLoss"
		admission.reason = semanticErr.Error()
		return admission, rpcSample{}, false
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
	switch operation.family {
	case receiptVertexPut:
		result, ok := receipt.GetOriginalResult().GetResult().(*graphv1.ReceiptResult_PutVertexOutcome)
		if !ok || result.PutVertexOutcome != graphv1.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
			return errors.New("confirmed receipt original result is not put_vertex_outcome=APPLIED_AND_LIVE")
		}
	case receiptVertexDelete:
		result, ok := receipt.GetOriginalResult().GetResult().(*graphv1.ReceiptResult_DeleteVertexExisted)
		if !ok || result.DeleteVertexExisted {
			return errors.New("confirmed receipt original result is not delete_vertex_existed=false")
		}
	case receiptEdgeDelete:
		result, ok := receipt.GetOriginalResult().GetResult().(*graphv1.ReceiptResult_DeleteEdgeExisted)
		if !ok || result.DeleteEdgeExisted {
			return errors.New("confirmed receipt original result is not delete_edge_existed=false")
		}
	case receiptEdgeAdd:
		result, ok := receipt.GetOriginalResult().GetResult().(*graphv1.ReceiptResult_AddEdgeEffectiveWeight)
		if !ok || math.Float32bits(result.AddEdgeEffectiveWeight) != operation.expectedAddWeightBits {
			return fmt.Errorf("confirmed AddEdge result does not match original response bits %08x", operation.expectedAddWeightBits)
		}
	default:
		return fmt.Errorf("unsupported receipt family %q", operation.family)
	}
	return nil
}

func appendCanonicalString(canonical []byte, value string) []byte {
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(value)))
	return append(canonical, value...)
}

func vertexPutIntentSHA256(vertex *graphv1.Vertex) ([sha256.Size]byte, error) {
	if vertex == nil || vertex.GetKey() == "" || vertex.GetExpiration() != nil {
		return [sha256.Size]byte{}, errors.New("receipt probe PutVertex requires a nonempty key and no expiration")
	}
	value, ok := vertex.GetValue().(*graphv1.Vertex_String_)
	if !ok {
		return [sha256.Size]byte{}, errors.New("receipt probe PutVertex requires a string value")
	}
	canonical := []byte{byte(mutationreceipt.PutVertex)}
	canonical = appendCanonicalString(canonical, vertex.GetKey())
	canonical = append(canonical, 17)
	canonical = appendCanonicalString(canonical, value.String_)
	canonical = append(canonical, 0, 1) // no expiration, if_absent=true
	return mutationreceipt.IntentDigest(canonical), nil
}

func vertexDeleteIntentSHA256(key string) [sha256.Size]byte {
	canonical := appendCanonicalString([]byte{byte(mutationreceipt.DeleteVertex)}, key)
	return mutationreceipt.IntentDigest(canonical)
}

func edgeDeleteIntentSHA256(tailKey, headKey string) [sha256.Size]byte {
	canonical := appendCanonicalString([]byte{byte(mutationreceipt.DeleteEdge)}, tailKey)
	canonical = appendCanonicalString(canonical, headKey)
	return mutationreceipt.IntentDigest(canonical)
}

func edgeAddIntentSHA256(edge *graphv1.Edge, contribID []byte) ([sha256.Size]byte, error) {
	if edge == nil || edge.GetTail() == "" || edge.GetHead() == "" || edge.GetExpiration() != nil ||
		math.IsInf(float64(edge.GetWeight()), 0) || math.IsNaN(float64(edge.GetWeight())) ||
		len(contribID) != len(mutationreceipt.ContribID{}) ||
		bytes.Equal(contribID, make([]byte, len(mutationreceipt.ContribID{}))) {
		return [sha256.Size]byte{}, errors.New("receipt probe AddEdge requires a finite source, no expiration, and a nonzero 24-byte ContribID")
	}
	canonical := appendCanonicalString([]byte{byte(mutationreceipt.AddEdge)}, edge.GetTail())
	canonical = appendCanonicalString(canonical, edge.GetHead())
	canonical = binary.BigEndian.AppendUint32(canonical, math.Float32bits(edge.GetWeight()))
	canonical = append(canonical, 0) // no expiration
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(contribID)))
	canonical = append(canonical, contribID...)
	return mutationreceipt.IntentDigest(canonical), nil
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
