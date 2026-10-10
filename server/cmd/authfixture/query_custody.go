package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/anaregdesign/lantern/core/privatefile"
	"io"
	"os"
	"strings"
	"time"
)

type queryShutdownNode struct {
	Node         int    `json:"node"`
	ExitCode     int    `json:"exit_code"`
	Passed       bool   `json:"passed"`
	FloorsSHA256 string `json:"floors_sha256,omitempty"`
	StateSHA256  string `json:"state_sha256,omitempty"`
	Cycle        uint64 `json:"cycle,omitempty"`
	Failure      string `json:"failure,omitempty"`
}

// Checkpoint observation is not resume qualification or a replacement for the
// production constructor. Exact original bindings and bytes remain private.
func checkQueryCustody(node queryCurrentNode) (queryShutdownNode, error) {
	var out queryShutdownNode
	var state struct {
		Version         uint32
		Phase           string
		Cycle           uint64
		Binding, Floors [32]byte
	}
	floors, err := readQueryPrivateFile(node.FloorsFile, 16<<10)
	if err != nil {
		return out, err
	}
	raw, err := readQueryPrivateFile(node.FloorsFile+".state", 16<<10)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(raw, &state) != nil || state.Version != 1 || state.Phase != "CLEAN" || state.Cycle != 1 || state.Binding == [32]byte{} || state.Binding != node.CustodyBinding || state.Floors != sha256.Sum256(floors) || !json.Valid(floors) {
		return out, errors.New("missing or mismatched original CLEAN/floors checkpoint")
	}
	out.FloorsSHA256, out.StateSHA256 = queryBindingHex(sha256.Sum256(floors)), queryBindingHex(sha256.Sum256(raw))
	out.Cycle = state.Cycle
	return out, nil
}

func readQueryPrivateFile(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > limit {
		return nil, errors.New("private regular query custody file required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) || privatefile.Check(file) != nil {
		return nil, errors.New("query custody path changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("bounded query custody file required")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) {
		return nil, errors.New("query custody path changed")
	}
	return raw, nil
}

func stopCurrentQueryChildren(f fixture, children []*fixtureProcess, dir string, budget time.Duration) error {
	var failure error
	deadline := time.Now().Add(budget)
	// Signal the entire owned cohort before waiting, retaining the common stop
	// budget rather than prematurely killing each child at the legacy timeout.
	for _, child := range children {
		select {
		case <-child.done:
		default:
			if err := child.command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
				failure = errors.Join(failure, err)
			}
		}
	}
	nodes := make([]queryShutdownNode, len(children))
	for i, child := range children {
		out := queryShutdownNode{Node: i + 1, ExitCode: -1}
		select {
		case <-child.done:
		default:
			timer := time.NewTimer(max(time.Until(deadline), time.Nanosecond))
			select {
			case <-child.done:
			case <-timer.C:
				_ = child.command.Process.Kill()
				<-child.done
				out.Failure = "shutdown_deadline"
			}
			timer.Stop()
		}

		if child.command.ProcessState != nil {
			out.ExitCode = child.command.ProcessState.ExitCode()
		}
		if err := child.log.Close(); err != nil {
			failure = errors.Join(failure, err)
		}
		if out.ExitCode != 0 && out.Failure == "" {
			out.Failure = "child_exit"
		}
		if out.Failure == "" {
			raw, err := os.ReadFile(child.log.Name())
			if err != nil || !strings.Contains(string(raw), "server stopped cleanly") {
				out.Failure = "missing_clean_shutdown_log"
			}
		}
		if out.Failure == "" && f.Query.Mode == "oidc" {
			if i >= len(f.Query.CurrentNodes) {
				out.Failure = "missing_original_custody"
			} else {
				checkpoint, err := checkQueryCustody(f.Query.CurrentNodes[i])
				if err != nil {
					out.Failure = "custody_mismatch"
				} else {
					out.FloorsSHA256, out.StateSHA256, out.Cycle = checkpoint.FloorsSHA256, checkpoint.StateSHA256, checkpoint.Cycle
				}
			}
		}
		out.Passed = out.Failure == ""
		if !out.Passed {
			failure = errors.Join(failure, fmt.Errorf("query node %d %s", i+1, out.Failure))
		}
		nodes[i] = out
	}
	if len(children) != 3 {
		failure = errors.Join(failure, errors.New("current query did not own three completed product children"))
	}
	receipt := map[string]any{"schema_version": 1, "security_profile": "current-v2", "mode": f.Query.Mode, "fixture_id": f.Query.FixtureID, "current_profile_binding": f.Query.CurrentBinding, "exporter_binary": f.Query.Exporter, "passed": failure == nil, "server_binary": f.Query.Server, "nodes": nodes}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return errors.Join(failure, err)
	}
	_, err = writeFile(dir, "query-shutdown.json", append(raw, '\n'))
	return errors.Join(failure, err)
}

func validQueryBinding(s string) bool {
	if !strings.HasPrefix(s, "current-v2:") || len(s) != 75 {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(s, "current-v2:"))
	return err == nil && len(raw) == sha256.Size
}
