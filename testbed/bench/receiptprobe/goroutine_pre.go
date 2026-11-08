package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

type goroutinePreReadiness struct {
	Schema   int    `json:"schema"`
	Protocol string `json:"protocol"`
	Verdict  string `json:"verdict"`
	Error    string `json:"error,omitempty"`
}

type goroutinePreAttempt struct {
	Index     int                `json:"index"`
	Runtime   goroutineReference `json:"runtime"`
	Profile   goroutineReference `json:"profile"`
	Readiness goroutineReference `json:"readiness"`
}

type goroutinePrePreparation struct {
	Schema          int                   `json:"schema"`
	Protocol        string                `json:"protocol"`
	SelectedAttempt int                   `json:"selected_attempt"`
	Attempts        []goroutinePreAttempt `json:"attempts"`
}

func validateGoroutinePrePreparation(ref goroutineReference, caseRoot string, baseline goroutineProfile, pre goroutineReference) error {
	if ref.Path != filepath.Join(caseRoot, "goroutine_policy_preparation.json") || !goroutineCaseReference(caseRoot, ref) {
		return errors.New("PRE preparation reference missing")
	}
	var preparation goroutinePrePreparation
	if goroutineDecode(ref, &preparation) != nil || preparation.Schema != 1 || preparation.Protocol != goroutineOperationalProtocol || preparation.SelectedAttempt < 1 || preparation.SelectedAttempt > 5 || len(preparation.Attempts) != preparation.SelectedAttempt {
		return errors.New("bounded PRE preparation absent")
	}
	parent := filepath.Join(caseRoot, "goroutine_policy_pre_attempts")
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != len(preparation.Attempts) {
		return errors.New("PRE attempt inventory differs")
	}
	for i, attempt := range preparation.Attempts {
		root := filepath.Join(parent, fmt.Sprintf("%03d", i+1))
		if attempt.Index != i+1 || !entries[i].IsDir() || entries[i].Name() != fmt.Sprintf("%03d", i+1) || !goroutinePhysicalCaseRoot(root) {
			return errors.New("PRE attempt order differs")
		}
		for name, input := range map[string]goroutineReference{"runtime_pre.json": attempt.Runtime, "goroutine_policy_baseline_profile.json": attempt.Profile, "readiness.json": attempt.Readiness} {
			if input.Path != filepath.Join(root, name) || !goroutineCaseReference(root, input) {
				return errors.New("PRE attempt evidence outside round")
			}
			if _, err := goroutineRead(input, 1<<20); err != nil {
				return err
			}
		}
		var profile goroutineProfile
		var readiness goroutinePreReadiness
		if goroutineDecode(attempt.Profile, &profile) != nil || !sameGoroutineProfile(baseline, profile) || goroutineDecode(attempt.Readiness, &readiness) != nil || readiness.Schema != 1 || readiness.Protocol != goroutineOperationalProtocol || readiness.Error != "" {
			return errors.New("PRE round identity or decision differs")
		}
		// Check complete observations, never favorable counts or memory values.
		var runtime []goroutineSnapshot
		if goroutineDecode(attempt.Runtime, &runtime) != nil || len(runtime) != len(profile.Nodes) {
			return errors.New("PRE round runtime incomplete")
		}
		for j, sample := range runtime {
			if sample.Endpoint != profile.Nodes[j].Endpoint || sample.Goroutines == nil || *sample.Goroutines <= 0 || sample.HeapAllocBytes == nil || *sample.HeapAllocBytes <= 0 {
				return errors.New("PRE round runtime incomplete")
			}
		}
		eligible, err := checkGoroutinePreTopology(profile, root)
		selected := i+1 == preparation.SelectedAttempt
		expected := "not_ready"
		if selected {
			expected = "eligible"
		}
		if err != nil || eligible != selected || readiness.Verdict != expected {
			return errors.New("PRE round was not first eligible")
		}
		if selected {
			if attempt.Runtime.SHA256 != pre.SHA256 {
				return errors.New("selected PRE runtime changed")
			}
			for j, node := range profile.Nodes {
				if node.PassiveMetrics.SHA256 != baseline.Nodes[j].PassiveMetrics.SHA256 {
					return errors.New("selected PRE passive response changed")
				}
			}
		}
	}
	return nil
}

// checkGoroutinePreTopology admits only the declared passive topology. Runtime
// goroutine/heap samples and their gate results never participate in selection.
func checkGoroutinePreTopology(profile goroutineProfile, caseRoot string) (bool, error) {
	if err := validateGoroutineProfile(profile, true); err != nil {
		return false, err
	}
	ready := true
	for _, node := range profile.Nodes {
		if !goroutineCaseReference(caseRoot, *node.PassiveMetrics) {
			return false, errors.New("PRE passive reference outside attempt")
		}
		raw, err := goroutineRead(*node.PassiveMetrics, 1<<20)
		if err != nil {
			return false, err
		}
		passive, err := parseGoroutinePassive(raw)
		if err != nil {
			return false, err
		}
		if len(passive.Peers) != len(node.ExpectedPeers) {
			return false, errors.New("PRE passive peer inventory differs from declared topology")
		}
		for _, peer := range node.ExpectedPeers {
			if _, ok := passive.Peers[peer]; !ok {
				return false, errors.New("PRE passive peer labels differ from declared topology")
			}
		}
		if !validateGoroutinePassive(passive, node) {
			ready = false
		}
	}
	return ready, nil
}

func runGoroutinePreCheck(args []string) int {
	fs := flag.NewFlagSet("check-goroutine-pre", flag.ContinueOnError)
	input := fs.String("input", "", "same-scrape PRE attempt profile")
	out := fs.String("out", "", "create-only topology admission report")
	if fs.Parse(args) != nil || *input == "" || *out == "" {
		return 2
	}
	root, err := goroutineCLIPaths(*input, *out)
	if err != nil {
		return 2
	}
	var profile goroutineProfile
	raw, err := readRetainedFile(*input, 256<<10)
	if err == nil {
		err = decodeGoroutineJSON(raw, &profile)
	}
	if err == nil {
		err = goroutineObserverMatches(profile)
	}
	ready := false
	if err == nil {
		ready, err = checkGoroutinePreTopology(profile, filepath.Clean(root))
	}
	verdict, code := "not_ready", 3
	if err != nil {
		verdict, code = "invalid", 2
	} else if ready {
		verdict, code = "eligible", 0
	}
	report := goroutinePreReadiness{Schema: 1, Protocol: goroutineOperationalProtocol, Verdict: verdict}
	if err != nil {
		report.Error = err.Error()
	}
	if writeGoroutineArtifact(*out, report) != nil {
		return 2
	}
	return code
}
