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
	for key, value := range overrides {
		if !strings.HasPrefix(key, "LANTERN_") || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid fixture override")
		}
		_, locked := node.Environment[key]
		if key == "LANTERN_PORT" || strings.HasPrefix(key, "LANTERN_TLS_") || strings.HasPrefix(key, "LANTERN_PEER_") {
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
		env["LANTERN_RECEIPT_WAL_MODE"] = "fresh"
		env["LANTERN_RECEIPT_WAL_PATH"] = filepath.Join(dir, result.Nodes[i].Name+"-receipts.wal")
		env["LANTERN_RECEIPT_EPOCH"] = hex.EncodeToString(epoch[:])
		env["LANTERN_RECEIPT_RETENTION"] = "1h"
		env["LANTERN_RECEIPT_MAX_ENTRIES"] = "512"
		env["LANTERN_RECEIPT_MAX_BYTES"] = "4194304"
	}
	return nil
}

func waitFixtureReady(ctx context.Context, result fixture, children []*fixtureProcess) error {
	started := time.Now()
	lastNode := 0
	probes, statuses := make([]string, len(result.Nodes)), make([]int, len(result.Nodes))
	for i := range probes {
		probes[i] = "none"
	}
	fail := func(reason string, cause error) error {
		diagnostic := fixtureReadinessDiagnostic{Reason: reason, Node: lastNode, ElapsedMillis: time.Since(started).Milliseconds(), Probe: "none", ServerLog: "unavailable"}
		if lastNode < len(result.Nodes) {
			diagnostic.Probe, diagnostic.HTTPStatus = probes[lastNode], statuses[lastNode]
			if origin, err := url.Parse(result.Nodes[lastNode].PublicOrigin); err == nil {
				port, _ := strconv.ParseUint(origin.Port(), 10, 16)
				diagnostic.Port = uint16(port)
			}
		}
		if lastNode < len(children) {
			child := children[lastNode]
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
			}
		}
		return &fixtureReadinessError{cause: cause, diagnostic: diagnostic}
	}
	pem, err := os.ReadFile(result.CAFile)
	if err != nil {
		return fail("ca_read", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return fail("ca_parse", errors.New("fixture CA is invalid"))
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	for i, node := range result.Nodes {
		if node.Environment["LANTERN_TLS_CLIENT_CA_FILE"] != "" {
			lastNode = i
			clientCert, err := tls.LoadX509KeyPair(result.ClientCertFile, result.ClientKeyFile)
			if err != nil {
				return fail("client_identity", err)
			}
			transport.TLSClientConfig.Certificates = []tls.Certificate{clientCert}
			break
		}
	}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := make([]string, len(result.Nodes))
	for {
		ready := true
		for i, node := range result.Nodes {
			lastNode = i
			select {
			case <-children[i].done:
				return fail("child_exit", fmt.Errorf("fixture node %d exited before readiness", i))
			default:
			}
			for _, path := range []string{"/grpc.health.v1.Health/Check", "/graph.v1.LanternSecurityService/GetAuthCapabilities"} {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, node.PublicOrigin+path, bytes.NewReader([]byte("{}")))
				if err != nil {
					return fail("request", err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Connect-Protocol-Version", "1")
				response, err := client.Do(req)
				if err != nil {
					probes[i], statuses[i] = fixtureProbeErrorCategory(err), 0
					ready = false
					break
				}
				var body struct {
					Status   string          `json:"status"`
					Ready    bool            `json:"ready"`
					Mode     string          `json:"mode"`
					Protocol uint32          `json:"protocolVersion"`
					Member   uint32          `json:"currentMember"`
					Profile  json.RawMessage `json:"currentProfile"`
				}
				decodeErr := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&body)
				_ = response.Body.Close()
				last[i] = fmt.Sprintf("node-%d %s HTTP=%d Health=%s ready=%t mode=%s version=%d", i, path, response.StatusCode, body.Status, body.Ready, body.Mode, body.Protocol)
				statuses[i] = response.StatusCode
				switch {
				case response.StatusCode != 200:
					probes[i] = "http"
				case decodeErr != nil:
					probes[i] = "json"
				case path == "/grpc.health.v1.Health/Check":
					probes[i] = "health"
				default:
					probes[i] = "capabilities"
				}
				if response.StatusCode != 200 || decodeErr != nil || path == "/grpc.health.v1.Health/Check" && body.Status != "SERVING_STATUS_SERVING" || path != "/grpc.health.v1.Health/Check" && (!body.Ready || !queryCapabilitiesReady(result.Query, i, body.Mode, body.Protocol, body.Member, body.Profile)) {
					ready = false
					break
				}
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fail("timeout", fmt.Errorf("fixture timed out before certified readiness: %s", strings.Join(last, "; ")))
		case <-ticker.C:
		}
	}
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
			if err := launch.configure(ctx, env); err != nil {
				return &fixtureFailure{stage: "spawn", cause: err}
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
