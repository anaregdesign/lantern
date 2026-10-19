package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// receiptHAControl isolates data replication. The quorum authority uses its
// own independently admitted native listeners; nodes one through three are A/B/C.
// This fixture is local conformance infrastructure, never a production proxy.
type receiptHAControl struct {
	connected    atomic.Bool
	identities   map[string]int
	fingerprints map[string][32]byte
}

func (c *receiptHAControl) allowed(caller, recipient int, path string) bool {
	if !strings.HasPrefix(path, "/graph.v1.LanternReplicationService/") {
		return false
	}
	if caller == 0 || recipient == 0 {
		return false
	}
	return c.connected.Load() || caller != 3 && recipient != 3
}
func (c *receiptHAControl) caller(state *tls.ConnectionState) (int, bool) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 || len(state.PeerCertificates[0].URIs) != 1 {
		return 0, false
	}
	leaf := state.PeerCertificates[0]
	identity := leaf.URIs[0].String()
	index, ok := c.identities[identity]
	return index, ok && c.fingerprints[identity] == sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
}
func receiptHACert(node fixtureNode) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(node.Environment["LANTERN_PEER_CERT_FILE"], node.Environment["LANTERN_PEER_KEY_FILE"])
	if err != nil {
		return cert, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	return cert, err
}

func receiptHASPKI(leaf *x509.Certificate) [32]byte {
	return sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
}

func serveReceiptHAFixture(ctx context.Context, result fixture, binary, directory string, input io.ReadCloser, output io.Writer, readyTimeout time.Duration) error {
	h := result.receiptLaunch
	if h == nil || !filepath.IsAbs(binary) || readyTimeout < time.Second || readyTimeout > 5*time.Minute {
		return errors.New("owned HA launch and bounded timeout required")
	}
	defer h.close()
	control := &receiptHAControl{}
	var err error
	h.relays, err = receiptHARelays(result, control, h.peers)
	if err != nil {
		return err
	}
	for _, node := range result.Nodes {
		for key, value := range map[string]string{"LANTERN_PUMP_BACKOFF_MIN_MS": "50", "LANTERN_PUMP_BACKOFF_MAX_MS": "200", "LANTERN_ANTI_ENTROPY_INTERVAL_MS": "250"} {
			node.Environment[key] = value
		}
	}
	if err = h.relays.resume(result); err != nil {
		return err
	}
	if err = h.configure(ctx, result); err != nil {
		return err
	}
	if err = h.public.resume(h.launches[3]); err != nil {
		return err
	}
	startup, cancel := context.WithDeadline(ctx, h.deadline)
	err = waitFixtureReady(startup, result, h.children())
	cancel()
	if err != nil {
		return err
	}
	if err = json.NewEncoder(output).Encode(result); err != nil {
		return err
	}
	commands := make(chan string, 1)
	commandCtx, stopCommands := context.WithCancel(ctx)
	commandsDone := make(chan struct{})
	defer func() { stopCommands(); _ = input.Close() }()
	go func() {
		defer close(commandsDone)
		readReceiptHACommands(commandCtx, input, commands)
	}()
	var stoppedA, relayedC bool
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case command, ok := <-commands:
			if !ok || command == "shutdown" {
				<-commandsDone
				return nil
			}
			switch command {
			case "stop-origin":
				if stoppedA {
					return errors.New("origin already stopped")
				}
				h.launches[1].close()
				stoppedA = true
			case "relay-b-to-c":
				if !stoppedA || relayedC {
					return errors.New("relay requires one explicit origin shutdown")
				}
				restart, cancel := context.WithTimeout(ctx, readyTimeout)
				err = restartReceiptHAC(restart, &result, binary, directory, control)
				cancel()
				if err != nil {
					return err
				}
				relayedC = true
			default:
				return errors.New("unknown receipt HA command")
			}
			if err = json.NewEncoder(output).Encode(map[string]string{"completed": command}); err != nil {
				return err
			}
		case <-ticker.C:
			for i, l := range h.launches {
				if stoppedA && i == 1 {
					continue
				}
				select {
				case <-l.process.done:
					return errors.New("owned receipt HA node exited")
				default:
				}
			}
		}
	}
}

// A shutdown frame is terminal: do not attempt another blocking stdin read
// while its caller still owns the pipe. On abort, main owns the stdin reader's
// process lifetime; socket workers and production children are joined by close.
func readReceiptHACommands(ctx context.Context, input io.Reader, commands chan<- string) {
	defer close(commands)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64), 1024)
	for scanner.Scan() {
		command := scanner.Text()
		select {
		case commands <- command:
		case <-ctx.Done():
			return
		}
		if command == "shutdown" {
			return
		}
	}
}

func restartReceiptHAC(ctx context.Context, result *fixture, binary, directory string, control *receiptHAControl) error {
	h := result.receiptLaunch
	h.public.pause()
	h.relays.pause()
	shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := h.launches[3].orderlyStop(shutdown)
	cancel()
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("bounded HA restart required")
	}
	launch, err := startFixtureLaunch(ctx, binary, directory, true, deadline)
	if err != nil {
		return err
	}
	h.launches[3] = launch // Register cleanup before configuring or retargeting.
	node := result.Nodes[3]
	node.Environment = maps.Clone(node.Environment)
	node.Environment["LANTERN_PORT"] = strconv.Itoa(launch.public)
	node.Environment["LANTERN_PEER_LISTEN_ADDR"] = net.JoinHostPort("127.0.0.1", strconv.Itoa(launch.peer))
	node.Environment["LANTERN_PEER_MEMBERSHIP_MODE"] = "resume"
	node.Environment["LANTERN_SECURITY_STORE_MODE"] = "resume"
	node.Environment["LANTERN_RECEIPT_WAL_MODE"] = "restart"
	if err = checkFixtureLaunchNode(launch, node); err != nil {
		return err
	}
	env, err := fixtureEnvironment(node, nil)
	if err != nil {
		return err
	}
	if err = launch.configure(ctx, env); err != nil {
		return fixtureAdoptionFailure(err, launch.process, 3, launch.public, 0)
	}
	result.Nodes[3] = node
	if err = h.relays.resume(*result); err != nil {
		return err
	}
	control.connected.Store(true)
	if err = h.public.resume(launch); err != nil {
		return err
	}
	return waitFixtureReady(ctx, fixture{CAFile: result.CAFile, Nodes: []fixtureNode{node}, authorityProfile: result.authorityProfile}, []*fixtureProcess{launch.process})
}
