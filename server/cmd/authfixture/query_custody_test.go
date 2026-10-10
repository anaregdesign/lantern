package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueryCustodyRequiresOriginalFreshCleanHashAndPrivateFiles(t *testing.T) {
	for _, kind := range []string{"valid", "running", "wrong binding", "wrong hash", "wrong cycle", "missing state", "public floors", "symlink floors"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "floors.json")
			floors := []byte(`{"M":{},"P":{},"B":{}}`)
			state := struct {
				Version         uint32
				Phase           string
				Cycle           uint64
				Binding, Floors [32]byte
			}{1, "CLEAN", 1, [32]byte{1}, sha256.Sum256(floors)}
			if kind == "running" {
				state.Phase = "RUNNING"
			}
			if kind == "wrong binding" {
				state.Binding[0] = 2
			}
			if kind == "wrong hash" {
				state.Floors[0] ^= 1
			}
			if kind == "wrong cycle" {
				state.Cycle = 2
			}
			if err := os.WriteFile(path, floors, 0600); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(state)
			if kind != "missing state" {
				if err := os.WriteFile(path+".state", raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "public floors" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink floors" {
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			}
			result, err := checkQueryCustody(queryCurrentNode{FloorsFile: path, CustodyBinding: [32]byte{1}})
			if (err == nil) != (kind == "valid") {
				t.Fatal(kind, err)
			}
			if err == nil && (len(result.FloorsSHA256) != 64 || len(result.StateSHA256) != 64 || result.Cycle != 1) {
				t.Fatal("checkpoint observation incomplete")
			}
		})
	}
}

func TestCurrentQueryShutdownCannotPassWithoutOwnedChildren(t *testing.T) {
	dir := t.TempDir()
	f := fixture{Query: &queryFixture{Mode: "off", SecurityProfile: "current-v2", FixtureID: strings.Repeat("1", 32)}}
	if stopCurrentQueryChildren(f, nil, dir, time.Millisecond) == nil {
		t.Fatal("empty owned cohort accepted")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "query-shutdown.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Passed bool                `json:"passed"`
		Nodes  []queryShutdownNode `json:"nodes"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.Passed || len(receipt.Nodes) != 0 {
		t.Fatal("failed shutdown observation lost")
	}
}
