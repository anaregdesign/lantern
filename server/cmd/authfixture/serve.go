package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/privatefile"
)

// fixtureProcess owns a production child and its private diagnostic log.
// done establishes the happens-before edge for exit; no caller invokes Wait twice.
type fixtureProcess struct {
	command *exec.Cmd
	log     *os.File
	done    chan struct{}
}

func (p *fixtureProcess) stop() {
	select {
	case <-p.done:
	default:
		_ = p.command.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			_ = p.command.Process.Kill()
			<-p.done
		}
	}
	_ = p.log.Close()
}

func fixtureEnvironment(node fixtureNode, overrides map[string]string) ([]string, error) {
	native := make(map[string]string, len(node.Environment)+len(overrides))
	for key, value := range node.Environment {
		native[key] = value
	}
	if node.StartupFile != "" {
		raw, err := os.ReadFile(node.StartupFile)
		if err != nil || len(raw) == 0 || len(raw) > 120<<10 {
			return nil, errors.New("required current fixture startup JSON unavailable")
		}
		native["LANTERN_SECURITY_CONFIG_JSON"] = string(raw)
	}
	for key, value := range overrides {
		if !strings.HasPrefix(key, "LANTERN_") || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid fixture override")
		}
		_, locked := node.Environment[key]
		if key == "LANTERN_PORT" || strings.HasPrefix(key, "LANTERN_TLS_") || strings.HasPrefix(key, "LANTERN_PEER_") || strings.HasPrefix(key, "LANTERN_SECURITY_") || strings.HasPrefix(key, "LANTERN_OIDC_") || strings.HasPrefix(key, "LANTERN_CDC_") {
			locked = true
		}
		if locked && key != "LANTERN_METRICS_ADDR" && key != "LANTERN_LOG_LEVEL" {
			return nil, errors.New("fixture trust, identity and listener settings cannot be overridden")
		}
		native[key] = value
	}
	result := make([]string, 0, len(os.Environ())+len(native))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "LANTERN_") {
			result = append(result, entry)
		}
	}
	keys := make([]string, 0, len(native))
	for key := range native {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+native[key])
	}
	return result, nil
}

func loadFixtureOverrides(path string, count int) ([]map[string]string, error) {
	result := make([]map[string]string, count)
	if path == "" {
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	if err := decoder.Decode(&result); err != nil || len(result) != count {
		return nil, errors.New("fixture overrides must be one bounded Object per node")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing fixture overrides")
	}
	return result, nil
}
func addFixtureReceipts(result *fixture, dir string) error {
	epoch, err := randomID()
	if err != nil {
		return err
	}
	for i := range result.Nodes {
		env := result.Nodes[i].Environment
		if env["LANTERN_AUTH_MODE"] != "oidc" {
			return errors.New("public receipt fixtures require OIDC admission")
		}
		delete(env, "LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR")
		delete(env, "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES")
		delete(env, "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES")
		env["LANTERN_RECEIPT_WAL_MODE"] = "fresh"
		env["LANTERN_RECEIPT_WAL_PATH"] = filepath.Join(dir, result.Nodes[i].Name+"-receipts.wal")
		env["LANTERN_RECEIPT_EPOCH"] = hex.EncodeToString(epoch[:])
		env["LANTERN_RECEIPT_RETENTION"] = "1h"
		env["LANTERN_RECEIPT_MAX_ENTRIES"] = "512"
		env["LANTERN_RECEIPT_MAX_BYTES"] = "4194304"
	}
	return nil
}

// fixtureReadinessPoll owns one complete probe pass and its retained local
// diagnostics. Polling cadence and the caller's external deadline remain with
// the outer wait; a completed pass never performs another diagnostic RPC.
type fixtureReadinessPoll struct {
	result                       fixture
	children                     []*fixtureProcess
	started                      time.Time
	lastNode                     int
	probes                       []string
	statuses                     []int
	health, capabilities         []string
	healthCount, capabilityCount []uint32
	observations                 []fixtureObservationReader
	last                         []string
	client                       *http.Client
	transport                    *http.Transport
}

func newFixtureReadinessPoll(result fixture, children []*fixtureProcess) (_ *fixtureReadinessPoll, err error) {
	p := &fixtureReadinessPoll{result: result, children: children, started: time.Now(),
		probes: make([]string, len(result.Nodes)), statuses: make([]int, len(result.Nodes)),
		health: make([]string, len(result.Nodes)), capabilities: make([]string, len(result.Nodes)),
		healthCount: make([]uint32, len(result.Nodes)), capabilityCount: make([]uint32, len(result.Nodes)),
		observations: make([]fixtureObservationReader, len(result.Nodes)), last: make([]string, len(result.Nodes))}
	for i := range p.probes {
		p.probes[i] = "none"
		p.health[i], p.capabilities[i] = "not_probed", "not_probed"
	}
	pem, err := os.ReadFile(p.result.CAFile)
	if err != nil {
		return nil, p.failure("ca_read", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, p.failure("ca_parse", errors.New("fixture CA is invalid"))
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
	p.transport = transport
	defer func() {
		if err != nil {
			p.close()
		}
	}()
	for i, node := range p.result.Nodes {
		requiresClient := node.Environment["LANTERN_TLS_CLIENT_CA_FILE"] != ""
		if node.StartupFile != "" {
			raw, err := os.ReadFile(node.StartupFile)
			var startup struct {
				TLS struct {
					ClientCA string `json:"client_ca_pem"`
				} `json:"tls"`
			}
			if err != nil || json.Unmarshal(raw, &startup) != nil {
				return nil, p.failure("client_identity", errors.New("current public TLS input unavailable"))
			}
			requiresClient = startup.TLS.ClientCA != ""
		}
		if requiresClient {
			p.lastNode = i
			clientCert, err := tls.LoadX509KeyPair(p.result.ClientCertFile, p.result.ClientKeyFile)
			if err != nil {
				return nil, p.failure("client_identity", err)
			}
			transport.TLSClientConfig.Certificates = []tls.Certificate{clientCert}
			break
		}
	}
	p.client = &http.Client{Transport: transport, Timeout: time.Second}
	return p, nil
}

func (p *fixtureReadinessPoll) close() {
	if p.transport != nil {
		p.transport.CloseIdleConnections()
	}
}

func (p *fixtureReadinessPoll) failure(reason string, cause error) error {
	diagnostic := fixtureReadinessDiagnostic{Reason: reason, Node: p.lastNode, ElapsedMillis: time.Since(p.started).Milliseconds(), Probe: "none", ServerLog: "unavailable", LastHealth: "not_probed", LastCapability: "not_probed", Observation: fixtureObservation{Status: "unavailable"}}
	if p.lastNode < len(p.result.Nodes) {
		diagnostic.Probe, diagnostic.HTTPStatus = p.probes[p.lastNode], p.statuses[p.lastNode]
		diagnostic.LastHealth, diagnostic.LastCapability = p.health[p.lastNode], p.capabilities[p.lastNode]
		diagnostic.HealthProbes, diagnostic.CapabilityProbes = p.healthCount[p.lastNode], p.capabilityCount[p.lastNode]
		if origin, err := url.Parse(p.result.Nodes[p.lastNode].PublicOrigin); err == nil {
			port, _ := strconv.ParseUint(origin.Port(), 10, 16)
			diagnostic.Port = uint16(port)
		}
	}
	if p.lastNode < len(p.children) {
		child := p.children[p.lastNode]
		select {
		case <-child.done:
			// Wait publishes ProcessState before closing done.
			diagnostic.ChildExited = true
			diagnostic.ChildExitCode = -1
			if child.command != nil && child.command.ProcessState != nil {
				diagnostic.ChildExitCode = child.command.ProcessState.ExitCode()
			}
		default:
		}
		if child.log != nil {
			diagnostic.ServerLog = fixtureServerLogCategory(child.log.Name())
			diagnostic.Observation = p.observations[p.lastNode].read(child.log.Name(), time.Now())
			diagnostic.Startup = fixtureStartupFailure(child.log.Name())
		}
	}
	return &fixtureReadinessError{cause: cause, diagnostic: diagnostic}
}

func (p *fixtureReadinessPoll) once(ctx context.Context) (bool, error) {
	ready := true
	for i, node := range p.result.Nodes {
		p.lastNode = i
		expectedMemberIndex := i
		if node.originalMember != 0 {
			expectedMemberIndex = int(node.originalMember) - 1
		}
		if p.children[i].log != nil {
			// Read only existing passive evidence; never call an extra RPC or
			// authority predicate to diagnose a failed Health response.
			_ = p.observations[i].read(p.children[i].log.Name(), time.Now())
		}
		select {
		case <-p.children[i].done:
			return false, p.failure("child_exit", fmt.Errorf("fixture node %d exited before readiness", i))
		default:
		}
		for _, path := range []string{"/grpc.health.v1.Health/Check", "/graph.v1.LanternSecurityService/GetAuthCapabilities"} {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, node.PublicOrigin+path, bytes.NewReader([]byte("{}")))
			if err != nil {
				return false, p.failure("request", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Connect-Protocol-Version", "1")
			isHealth := path == "/grpc.health.v1.Health/Check"
			category := &p.capabilities[i]
			if isHealth {
				category = &p.health[i]
				p.healthCount[i]++
			} else {
				p.capabilityCount[i]++
			}
			response, err := p.client.Do(req)
			if err != nil {
				*category = "transport_failure"
				p.probes[i], p.statuses[i] = fixtureProbeErrorCategory(err), 0
				ready = false
				break
			}
			var body struct {
				Status   string          `json:"status"`
				Ready    *bool           `json:"ready"`
				Mode     string          `json:"mode"`
				Protocol uint32          `json:"protocolVersion"`
				Member   uint32          `json:"currentMember"`
				Profile  json.RawMessage `json:"currentProfile"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&body)
			_ = response.Body.Close()
			p.last[i] = fmt.Sprintf("node-%d %s HTTP=%d Health=%s mode=%s version=%d", i, path, response.StatusCode, body.Status, body.Mode, body.Protocol)
			p.statuses[i] = response.StatusCode
			switch {
			case response.StatusCode != 200:
				p.probes[i] = "http"
				*category = "http_failure"
			case decodeErr != nil:
				p.probes[i] = "json"
				*category = "decode_failure"
			case isHealth:
				p.probes[i] = "health"
				*category = fixtureHealthCategory(body.Status)
			default:
				p.probes[i] = "capabilities"
				switch {
				case body.Ready == nil:
					*category = "missing"
				case !*body.Ready:
					*category = "not_ready"
				case node.originalMember > 31 || node.originalMember != 0 && p.result.authorityProfile == nil && p.result.Query == nil || !queryCapabilitiesReady(p.result.Query, expectedMemberIndex, body.Mode, body.Protocol, body.Member, body.Profile, p.result.authorityProfile):
					*category = "uncertified"
				default:
					*category = "ready"
				}
			}
			if response.StatusCode != 200 || decodeErr != nil || isHealth && body.Status != "SERVING_STATUS_SERVING" || !isHealth && *category != "ready" {
				ready = false
				break
			}
		}
	}
	return ready, nil
}

func waitFixtureReady(ctx context.Context, result fixture, children []*fixtureProcess) error {
	poll, err := newFixtureReadinessPoll(result, children)
	if err != nil {
		return err
	}
	defer poll.close()
	return poll.wait(ctx)
}

func (poll *fixtureReadinessPoll) wait(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := poll.once(ctx)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return poll.failure("timeout", fmt.Errorf("fixture timed out before certified readiness: %s", strings.Join(poll.last, "; ")))
		case <-ticker.C:
		}
	}
}

func initializeFixtureNodes(ctx context.Context, result *fixture, binary, dir string, overrides []map[string]string) error {
	// Explicit initialization is a distinct native owner for each original node.
	// Complete all initializations before opening any serving authority.
	for i, node := range result.Nodes {
		if node.StartupFile == "" || node.initialized {
			continue
		}
		env, err := fixtureEnvironment(node, fixtureNodeOverrides(overrides, i))
		if err != nil {
			return err
		}
		log, err := privatefile.Create(filepath.Join(dir, node.Name+"-init.log"), os.O_WRONLY)
		if err != nil {
			return err
		}
		command := exec.CommandContext(ctx, binary, "init-security")
		command.Env, command.Stdout, command.Stderr = env, log, log
		runErr := command.Run()
		closeErr := log.Close()
		if runErr != nil || closeErr != nil {
			return errors.Join(errors.New("explicit fixture security initialization failed; retain private init log"), runErr, closeErr)
		}
		result.Nodes[i].initialized = true
	}
	return nil
}

func fixtureNodeOverrides(overrides []map[string]string, i int) map[string]string {
	if len(overrides) > i {
		return overrides[i]
	}
	return nil
}

func fixtureProbeErrorCategory(err error) string {
	var certificate *tls.CertificateVerificationError
	var network net.Error
	var operation *net.OpError
	switch {
	case errors.As(err, &certificate):
		return "tls_certificate"
	case errors.As(err, &network) && network.Timeout():
		return "timeout"
	case errors.As(err, &operation) && operation.Op == "dial":
		return "dial"
	default:
		return "transport"
	}
}

// serveFixture publishes metadata only after verified-TLS production readiness,
// then supervises every child until cancellation or an explicit stdin shutdown.
// EOF does not stop detached CI supervision. Startup failures always reap children.
func serveFixture(ctx context.Context, result fixture, binary, dir, overridesFile string, input io.Reader, output io.Writer, readyTimeout time.Duration) (resultErr error) {
	if len(result.launches) != 0 && len(result.launches) != len(result.Nodes) || len(result.Nodes) == 0 || len(result.Nodes) > 8 || !filepath.IsAbs(binary) || readyTimeout < time.Second || readyTimeout > 5*time.Minute {
		return errors.New("absolute Server binary and bounded readiness timeout required")
	}
	overrides, err := loadFixtureOverrides(overridesFile, len(result.Nodes))
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64), 1024)
		for scanner.Scan() {
			if scanner.Text() == "shutdown" {
				stop()
				return
			}
		}
	}()

	children := make([]*fixtureProcess, 0, len(result.Nodes))
	defer func() {
		if result.Query != nil && result.Query.SecurityProfile == "current-v2" {
			resultErr = errors.Join(resultErr, stopCurrentQueryChildren(result, children, dir, 90*time.Second))
			return
		}
		if len(result.launches) != 0 {
			closeFixtureLaunches(result.launches)
			return
		}
		for i := len(children) - 1; i >= 0; i-- {
			children[i].stop()
		}
	}()
	initialization, stopInitialization := context.WithTimeout(ctx, readyTimeout)
	if len(result.launches) != 0 {
		stopInitialization()
		initialization, stopInitialization = context.WithDeadline(ctx, result.launches[0].deadline)
	}
	err = initializeFixtureNodes(initialization, &result, binary, dir, overrides)
	stopInitialization()
	if err != nil {
		return err
	}
	for i, node := range result.Nodes {
		env, err := fixtureEnvironment(node, overrides[i])
		if err != nil {
			return err
		}
		if len(result.launches) != 0 {
			launch := result.launches[i]
			if err := checkFixtureLaunchNode(launch, node); err != nil {
				return err
			}
			children = append(children, launch.process)
			started := time.Now()
			if err := launch.configure(ctx, env); err != nil {
				return fixtureAdoptionFailure(err, launch.process, i, launch.public, time.Since(started))
			}
			continue
		}
		log, err := privatefile.Create(filepath.Join(dir, node.Name+"-server.log"), os.O_WRONLY)
		if err != nil {
			return err
		}
		command := exec.Command(binary)
		command.Env = env
		command.Stdout = log
		command.Stderr = log
		if err := command.Start(); err != nil {
			_ = log.Close()
			return &fixtureFailure{stage: "spawn", cause: err}
		}
		process := &fixtureProcess{command: command, log: log, done: make(chan struct{})}
		children = append(children, process)
		go func() { _ = command.Wait(); close(process.done) }()
	}
	startup, cancel := context.WithTimeout(ctx, readyTimeout)
	if len(result.launches) != 0 {
		cancel()
		startup, cancel = context.WithDeadline(ctx, result.launches[0].deadline)
	}
	err = waitFixtureReady(startup, result, children)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return &fixtureFailure{stage: "readiness", cause: err}
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return &fixtureFailure{stage: "publication", cause: err}
	}

	// Also stop the cohort if a child exits after readiness.
	failure := make(chan struct{}, len(children))
	for _, child := range children {
		go func() { <-child.done; failure <- struct{}{} }()
	}
	select {
	case <-ctx.Done():
		return nil
	case <-failure:
		return &fixtureFailure{stage: "exit", cause: errors.New("production fixture node exited")}
	}
}
