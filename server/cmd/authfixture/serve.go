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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
		if _, locked := node.Environment[key]; locked && key != "LANTERN_METRICS_ADDR" && key != "LANTERN_LOG_LEVEL" {
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
	pem, err := os.ReadFile(result.CAFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return errors.New("fixture CA is invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	for _, node := range result.Nodes {
		if node.Environment["LANTERN_TLS_CLIENT_CA_FILE"] != "" {
			clientCert, err := tls.LoadX509KeyPair(result.ClientCertFile, result.ClientKeyFile)
			if err != nil {
				return err
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
			select {
			case <-children[i].done:
				return fmt.Errorf("fixture node %d exited before readiness", i)
			default:
			}
			for _, path := range []string{"/grpc.health.v1.Health/Check", "/graph.v1.LanternSecurityService/GetAuthCapabilities"} {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, node.PublicOrigin+path, bytes.NewReader([]byte("{}")))
				if err != nil {
					return err
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Connect-Protocol-Version", "1")
				response, err := client.Do(req)
				if err != nil {
					ready = false
					break
				}
				var body struct {
					Status   string `json:"status"`
					Ready    bool   `json:"ready"`
					Mode     string `json:"mode"`
					Protocol uint32 `json:"protocolVersion"`
				}
				decodeErr := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&body)
				_ = response.Body.Close()
				last[i] = fmt.Sprintf("node-%d %s HTTP=%d Health=%s ready=%t mode=%s version=%d", i, path, response.StatusCode, body.Status, body.Ready, body.Mode, body.Protocol)
				if response.StatusCode != 200 || decodeErr != nil || path == "/grpc.health.v1.Health/Check" && body.Status != "SERVING_STATUS_SERVING" || path != "/grpc.health.v1.Health/Check" && (!body.Ready || body.Protocol != 1 || body.Mode != "AUTH_MODE_OFF" && body.Mode != "AUTH_MODE_OIDC") {
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
			return fmt.Errorf("fixture timed out before certified readiness: %s", strings.Join(last, "; "))
		case <-ticker.C:
		}
	}
}

// serveFixture publishes metadata only after verified-TLS production readiness,
// then supervises every child until cancellation or an explicit stdin shutdown.
// EOF does not stop detached CI supervision. Startup failures always reap children.
func serveFixture(ctx context.Context, result fixture, binary, dir, overridesFile string, input io.Reader, output io.Writer, readyTimeout time.Duration) error {
	if len(result.Nodes) == 0 || len(result.Nodes) > 8 || !filepath.IsAbs(binary) || readyTimeout < time.Second || readyTimeout > 5*time.Minute {
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
		for i := len(children) - 1; i >= 0; i-- {
			children[i].stop()
		}
	}()
	for i, node := range result.Nodes {
		env, err := fixtureEnvironment(node, overrides[i])
		if err != nil {
			return err
		}
		log, err := os.OpenFile(filepath.Join(dir, node.Name+"-server.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		command := exec.Command(binary)
		command.Env = env
		command.Stdout = log
		command.Stderr = log
		if err := command.Start(); err != nil {
			_ = log.Close()
			return errors.New("cannot start production fixture node")
		}
		process := &fixtureProcess{command: command, log: log, done: make(chan struct{})}
		children = append(children, process)
		go func() { _ = command.Wait(); close(process.done) }()
	}
	startup, cancel := context.WithTimeout(ctx, readyTimeout)
	err = waitFixtureReady(startup, result, children)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return err
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
		return errors.New("production fixture node exited")
	}
}
