package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestRequireLivePutResults(t *testing.T) {
	if err := requireLiveVertexResults([]client.VertexPutResult{{Key: "v", Outcome: client.PutOutcomeAppliedAndLive}}); err != nil {
		t.Fatalf("live vertex result: %v", err)
	}
	if err := requireLiveVertexResults([]client.VertexPutResult{{Key: "v", Outcome: client.PutOutcomeExpired}}); err == nil {
		t.Fatal("expired vertex result was reported as successful")
	}
	if err := requireLiveEdgeResults([]client.EdgePutResult{{Tail: "a", Head: "b", Outcome: client.PutOutcomeAppliedAndLive}}); err != nil {
		t.Fatalf("live edge result: %v", err)
	}
	if err := requireLiveEdgeResults([]client.EdgePutResult{{Tail: "a", Head: "b", Outcome: client.PutOutcomeSuperseded}}); err == nil {
		t.Fatal("superseded edge result was reported as successful")
	}
}

// FuzzBulkVertexLine fuzzes the per-line decode pipeline of `bulk vertices`:
// json.Unmarshal into vertexLine, then parseTTL + expirationFromTTL. A
// malformed NDJSON line must surface as an error, never a panic — the CLI
// streams untrusted files straight into this path.
func FuzzBulkVertexLine(f *testing.F) {
	seeds := []string{
		`{"key":"alice","value":42}`,
		`{"key":"bob","value":{"name":"Bob"},"ttl":"1h"}`,
		`{"key":"c","value":"hi","ttl":"30m"}`,
		`{"key":"d","value":[1,2,3]}`,
		`{"key":"e","value":true,"ttl":"0s"}`,
		`{"key":"f","value":1.5,"ttl":"-5s"}`,
		`{"ttl":"notaduration"}`,
		`{`,
		``,
		`null`,
		`{"value":1e999}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		var v vertexLine
		if json.Unmarshal([]byte(line), &v) != nil {
			return
		}
		ttl, err := parseTTL(v.TTL)
		if err != nil {
			return
		}
		_ = expirationFromTTL(ttl)
	})
}

// FuzzBulkEdgeLine fuzzes the `bulk edges` per-line decode pipeline.
func FuzzBulkEdgeLine(f *testing.F) {
	seeds := []string{
		`{"tail":"alice","head":"bob","weight":1.5}`,
		`{"tail":"a","head":"b","weight":-0.0,"ttl":"1h"}`,
		`{"tail":"a","head":"b"}`,
		`{"weight":"notafloat"}`,
		`{"tail":"a","head":"b","weight":1,"ttl":"bogus"}`,
		`{`,
		``,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		var e edgeLine
		if json.Unmarshal([]byte(line), &e) != nil {
			return
		}
		ttl, err := parseTTL(e.TTL)
		if err != nil {
			return
		}
		_ = expirationFromTTL(ttl)
	})
}

func TestBulkEdgeBlindAcknowledgementAndLaterFailure(t *testing.T) {
	for _, verb := range []string{"add", "put"} {
		for _, failAt := range []int{0, 2} {
			t.Run(verb+string(rune('0'+failAt)), func(t *testing.T) {
				wire := commandMutationFixture(t, failAt)
				path := filepath.Join(t.TempDir(), "edges.ndjson")
				raw := strings.Repeat(`{"tail":"a","head":"b","weight":1}`+"\n", 3)
				if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
				var output, progress bytes.Buffer
				cmd := newBulkEdgesCmd(verb)
				cmd.SetContext(t.Context())
				cmd.SetOut(&output)
				cmd.SetErr(&progress)
				err := cmd.RunE(cmd, []string{path})
				if failAt == 0 {
					if err != nil || wire.calls != 3 || output.String() != "{\"acceptance\":\"acceptedUndisclosed\"}\n" {
						t.Fatal("bulk blind acknowledgement", err, wire.calls, output.String())
					}
				} else if err == nil || wire.calls != 2 || output.Len() != 0 {
					t.Fatal("partial failure became complete acceptance", err, wire.calls, output.String())
				}
			})
		}
	}
}
