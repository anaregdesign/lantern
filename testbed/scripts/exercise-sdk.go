// exercise-sdk drives every public method of github.com/anaregdesign/lantern/sdks/go
// against the running testbed (localhost:6380). Run via `go run` from the repo
// root so we use the workspace replace.
//
//	go run ./testbed/scripts/exercise-sdk.go
//
// Writes a structured report to testbed/out/sdk/report.txt.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	client "github.com/anaregdesign/lantern/sdks/go"
)

const (
	addr = "http://localhost:6380"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}

func run() error {
	outDir := mustOutDir()
	reportPath := filepath.Join(outDir, "report.txt")
	f, err := os.Create(reportPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := io.MultiWriter(os.Stdout, f)

	step := func(name string, body func() error) {
		_, _ = fmt.Fprintf(w, "==> %s\n", name)
		if err := body(); err != nil {
			_, _ = fmt.Fprintf(w, "    ERR: %v\n", err)
			return
		}
		_, _ = fmt.Fprintf(w, "    ok\n")
	}

	// ---- client construction with every option ---------------------
	lc, err := client.NewLantern(
		addr,
		client.WithDefaultTimeout(5*time.Second),
		client.WithBatchChunkSize(3), // tiny chunk to exercise chunking
		// Compression goes through Connect's send-compression option;
		// the gzip codec is auto-registered by the Connect runtime.
		client.WithConnectClientOption(connect.WithSendCompression("gzip")),
	)
	if err != nil {
		return fmt.Errorf("NewLantern: %w", err)
	}
	defer func() { _ = lc.Close() }()

	ctx := context.Background()

	step("Ping", func() error { return lc.Ping(ctx) })

	// ---- Put/Get/Delete single vertex ------------------------------
	step("PutVertex string", func() error {
		return lc.PutVertex(ctx, "sdk:alice", "Alice", 5*time.Minute)
	})
	step("PutVertex int", func() error {
		return lc.PutVertex(ctx, "sdk:count", int64(42), 5*time.Minute)
	})
	step("PutVertex bool", func() error {
		return lc.PutVertex(ctx, "sdk:flag", true, 5*time.Minute)
	})
	step("PutVertex float", func() error {
		return lc.PutVertex(ctx, "sdk:pi", 3.14, 5*time.Minute)
	})
	step("PutVertex bytes", func() error {
		return lc.PutVertex(ctx, "sdk:blob", []byte("\x00\x01\x02"), 5*time.Minute)
	})
	step("PutVertex time", func() error {
		return lc.PutVertex(ctx, "sdk:ts", time.Unix(1700000000, 0).UTC(), 5*time.Minute)
	})
	step("PutVertex duration", func() error {
		return lc.PutVertex(ctx, "sdk:dur", 30*time.Second, 5*time.Minute)
	})
	step("PutVertexAt", func() error {
		return lc.PutVertexAt(ctx, "sdk:abs", "absolute-exp", time.Now().Add(2*time.Minute))
	})
	step("PutVertex nil", func() error {
		return lc.PutVertex(ctx, "sdk:nil", nil, 5*time.Minute)
	})

	step("GetVertex string", func() error {
		v, err := lc.GetVertex(ctx, "sdk:alice")
		if err != nil {
			return err
		}
		got, err := client.StringValue(v)
		if err != nil {
			return err
		}
		if got != "Alice" {
			return fmt.Errorf("want Alice, got %q", got)
		}
		return nil
	})
	step("GetVertex int", func() error {
		v, err := lc.GetVertex(ctx, "sdk:count")
		if err != nil {
			return err
		}
		got, err := client.IntValue(v)
		if err != nil {
			return err
		}
		if got != 42 {
			return fmt.Errorf("want 42, got %d", got)
		}
		return nil
	})
	step("GetVertex bytes", func() error {
		v, err := lc.GetVertex(ctx, "sdk:blob")
		if err != nil {
			return err
		}
		b, err := client.BytesValue(v)
		if err != nil {
			return err
		}
		if len(b) != 3 {
			return fmt.Errorf("want 3 bytes, got %d", len(b))
		}
		return nil
	})
	step("GetVertex time", func() error {
		v, err := lc.GetVertex(ctx, "sdk:ts")
		if err != nil {
			return err
		}
		t, err := client.TimeValue(v)
		if err != nil {
			return err
		}
		if t.Unix() != 1700000000 {
			return fmt.Errorf("want 1700000000, got %d", t.Unix())
		}
		return nil
	})
	step("GetVertex nil", func() error {
		v, err := lc.GetVertex(ctx, "sdk:nil")
		if err != nil {
			return err
		}
		if !client.IsNil(v) {
			return fmt.Errorf("expected IsNil true")
		}
		return nil
	})

	// ---- expected ErrNotFound --------------------------------------
	step("GetVertex missing → ErrNotFound", func() error {
		_, err := lc.GetVertex(ctx, "sdk:does-not-exist")
		if !errors.Is(err, client.ErrNotFound) {
			return fmt.Errorf("want ErrNotFound, got %v", err)
		}
		return nil
	})

	// ---- expected ErrInvalidArgument (empty key) -------------------
	step("PutVertex empty key → ErrInvalidArgument", func() error {
		err := lc.PutVertex(ctx, "", "x", time.Minute)
		if !errors.Is(err, client.ErrInvalidArgument) {
			return fmt.Errorf("want ErrInvalidArgument, got %v", err)
		}
		return nil
	})

	// ---- DeleteVertex ----------------------------------------------
	step("DeleteVertex existing", func() error {
		existed, err := lc.DeleteVertex(ctx, "sdk:abs")
		if err != nil {
			return err
		}
		if !existed {
			return fmt.Errorf("want existed=true")
		}
		return nil
	})
	step("DeleteVertex missing", func() error {
		existed, err := lc.DeleteVertex(ctx, "sdk:abs")
		if err != nil {
			return err
		}
		if existed {
			return fmt.Errorf("want existed=false")
		}
		return nil
	})

	// ---- batch helpers (chunk size = 3, supply 7 entries) ----------
	step("PutVertices batch (chunked)", func() error {
		var in []client.VertexInput
		for i := 0; i < 7; i++ {
			in = append(in, client.VertexInput{
				Key:        fmt.Sprintf("sdk:batch-%d", i),
				Value:      int64(i),
				Expiration: time.Now().Add(5 * time.Minute),
			})
		}
		return lc.PutVertices(ctx, in)
	})
	step("GetVertices mixed", func() error {
		keys := []string{"sdk:batch-0", "sdk:batch-3", "sdk:batch-6", "sdk:nope"}
		found, missing, err := lc.GetVertices(ctx, keys)
		if err != nil {
			return err
		}
		if len(found) != 3 || len(missing) != 1 || missing[0] != "sdk:nope" {
			return fmt.Errorf("want 3 found / 1 missing, got %d / %v", len(found), missing)
		}
		return nil
	})
	step("DeleteVertices batch", func() error {
		var keys []string
		for i := 0; i < 7; i++ {
			keys = append(keys, fmt.Sprintf("sdk:batch-%d", i))
		}
		n, err := lc.DeleteVertices(ctx, keys)
		if err != nil {
			return err
		}
		if n != 7 {
			return fmt.Errorf("want 7 deleted, got %d", n)
		}
		return nil
	})

	// ---- edges -----------------------------------------------------
	step("AddEdge then AddEdge (additive)", func() error {
		if err := lc.AddEdge(ctx, "sdk:a", "sdk:b", 1.0, 5*time.Minute); err != nil {
			return err
		}
		if err := lc.AddEdge(ctx, "sdk:a", "sdk:b", 0.5, 5*time.Minute); err != nil {
			return err
		}
		e, err := lc.GetEdge(ctx, "sdk:a", "sdk:b")
		if err != nil {
			return err
		}
		if e.Weight < 1.49 || e.Weight > 1.51 {
			return fmt.Errorf("want ~1.5, got %v", e.Weight)
		}
		return nil
	})
	step("PutEdge (idempotent replace)", func() error {
		if err := lc.PutEdge(ctx, "sdk:a", "sdk:b", 0.25, 5*time.Minute); err != nil {
			return err
		}
		e, err := lc.GetEdge(ctx, "sdk:a", "sdk:b")
		if err != nil {
			return err
		}
		if e.Weight != 0.25 {
			return fmt.Errorf("want 0.25, got %v", e.Weight)
		}
		return nil
	})
	step("PutEdgeAt", func() error {
		return lc.PutEdgeAt(ctx, "sdk:a", "sdk:c", 0.7, time.Now().Add(2*time.Minute))
	})
	step("AddEdgeAt", func() error {
		return lc.AddEdgeAt(ctx, "sdk:a", "sdk:d", 0.3, time.Now().Add(2*time.Minute))
	})
	step("GetEdge missing → ErrNotFound", func() error {
		_, err := lc.GetEdge(ctx, "sdk:a", "sdk:nobody")
		if !errors.Is(err, client.ErrNotFound) {
			return fmt.Errorf("want ErrNotFound, got %v", err)
		}
		return nil
	})

	// ---- AddEdges / PutEdges (chunked) -----------------------------
	step("AddEdges batch (chunked)", func() error {
		var in []client.EdgeInput
		for i := 0; i < 5; i++ {
			in = append(in, client.EdgeInput{
				Tail:       "sdk:hub",
				Head:       fmt.Sprintf("sdk:spoke-%d", i),
				Weight:     float32(i + 1),
				Expiration: time.Now().Add(5 * time.Minute),
			})
		}
		return lc.AddEdges(ctx, in)
	})
	step("PutEdges batch (chunked)", func() error {
		var in []client.EdgeInput
		for i := 0; i < 5; i++ {
			in = append(in, client.EdgeInput{
				Tail:       "sdk:hub",
				Head:       fmt.Sprintf("sdk:spoke-%d", i),
				Weight:     0.1,
				Expiration: time.Now().Add(5 * time.Minute),
			})
		}
		return lc.PutEdges(ctx, in)
	})

	// ---- GetEdges / DeleteEdges ------------------------------------
	step("GetEdges mixed", func() error {
		refs := []client.EdgeRef{
			{Tail: "sdk:hub", Head: "sdk:spoke-0"},
			{Tail: "sdk:hub", Head: "sdk:spoke-4"},
			{Tail: "sdk:hub", Head: "sdk:nobody"},
		}
		found, missing, err := lc.GetEdges(ctx, refs)
		if err != nil {
			return err
		}
		if len(found) != 2 || len(missing) != 1 {
			return fmt.Errorf("want 2 found / 1 missing, got %d / %v", len(found), missing)
		}
		return nil
	})
	step("DeleteEdges batch", func() error {
		var refs []client.EdgeRef
		for i := 0; i < 5; i++ {
			refs = append(refs, client.EdgeRef{Tail: "sdk:hub", Head: fmt.Sprintf("sdk:spoke-%d", i)})
		}
		refs = append(refs,
			client.EdgeRef{Tail: "sdk:a", Head: "sdk:b"},
			client.EdgeRef{Tail: "sdk:a", Head: "sdk:c"},
			client.EdgeRef{Tail: "sdk:a", Head: "sdk:d"},
		)
		n, err := lc.DeleteEdges(ctx, refs)
		if err != nil {
			return err
		}
		if n < 5 {
			return fmt.Errorf("want >=5 deleted, got %d", n)
		}
		return nil
	})

	// ---- Illuminate every Optimization ------------------------------
	// Re-build a small graph: alice -> bob (0.9), alice -> carol (0.1), bob -> dave (0.8), carol -> dave (0.2)
	step("seed illuminate graph", func() error {
		now := time.Now().Add(5 * time.Minute)
		if err := lc.PutVertices(ctx, []client.VertexInput{
			{Key: "ill:alice", Value: "A", Expiration: now},
			{Key: "ill:bob", Value: "B", Expiration: now},
			{Key: "ill:carol", Value: "C", Expiration: now},
			{Key: "ill:dave", Value: "D", Expiration: now},
		}); err != nil {
			return err
		}
		return lc.PutEdges(ctx, []client.EdgeInput{
			{Tail: "ill:alice", Head: "ill:bob", Weight: 0.9, Expiration: now},
			{Tail: "ill:alice", Head: "ill:carol", Weight: 0.1, Expiration: now},
			{Tail: "ill:bob", Head: "ill:dave", Weight: 0.8, Expiration: now},
			{Tail: "ill:carol", Head: "ill:dave", Weight: 0.2, Expiration: now},
		})
	})
	for _, c := range []struct {
		name string
		red  client.Reduction
		obj  client.Objective
	}{
		{"none", client.ReductionNone, client.ObjectiveUnspecified},
		{"mst/min", client.ReductionMinimumSpanningTree, client.ObjectiveMinimize},
		{"mst/max", client.ReductionMinimumSpanningTree, client.ObjectiveMaximize},
		{"spt/min", client.ReductionShortestPathTree, client.ObjectiveMinimize},
		{"spt/max", client.ReductionShortestPathTree, client.ObjectiveMaximize},
	} {
		c := c
		step("Illuminate "+c.name, func() error {
			g, err := lc.Illuminate(ctx, "ill:alice",
				client.WithBFS(client.BFSOpts{Step: 3, FanOut: 10, Objective: c.obj, Reduction: c.red}),
			)
			if err != nil {
				return err
			}
			if len(g.Vertices) == 0 {
				return fmt.Errorf("empty vertex set")
			}
			return nil
		})
	}
	step("Illuminate TFIDF", func() error {
		g, err := lc.Illuminate(ctx, "ill:alice",
			client.WithBFS(client.BFSOpts{Step: 2, FanOut: 5}), client.WithWeighting(client.WeightingTFIDF),
		)
		if err != nil {
			return err
		}
		if len(g.Vertices) == 0 {
			return fmt.Errorf("empty vertex set")
		}
		return nil
	})

	// ---- short TTL → confirm expiry ---------------------------------
	step("ephemeral vertex expiry", func() error {
		if err := lc.PutVertex(ctx, "sdk:ephem", "bye", 2*time.Second); err != nil {
			return err
		}
		time.Sleep(3 * time.Second)
		_, err := lc.GetVertex(ctx, "sdk:ephem")
		if !errors.Is(err, client.ErrNotFound) {
			return fmt.Errorf("want ErrNotFound after TTL, got %v", err)
		}
		return nil
	})

	// ---- cleanup ----------------------------------------------------
	step("cleanup vertices", func() error {
		_, err := lc.DeleteVertices(ctx, []string{
			"sdk:alice", "sdk:count", "sdk:flag", "sdk:pi",
			"sdk:blob", "sdk:ts", "sdk:dur", "sdk:nil",
			"ill:alice", "ill:bob", "ill:carol", "ill:dave",
			"sdk:hub", "sdk:a", "sdk:b", "sdk:c", "sdk:d",
		})
		return err
	})

	_, _ = fmt.Fprintf(w, "\nSDK report written to %s\n", reportPath)
	return nil
}

func mustOutDir() string {
	// resolve testbed/out/sdk relative to the script's own location so
	// "go run ./testbed/scripts/exercise-sdk.go" works from anywhere.
	exe, _ := os.Executable()
	_ = exe // not reliable inside `go run`; instead walk up from cwd.
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	// look for testbed/ in or above cwd
	for d := cwd; d != "/"; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, "testbed")); err == nil && fi.IsDir() {
			out := filepath.Join(d, "testbed", "out", "sdk")
			must(os.MkdirAll(out, 0o755))
			return out
		}
		if strings.HasSuffix(d, "/testbed") {
			out := filepath.Join(d, "out", "sdk")
			must(os.MkdirAll(out, 0o755))
			return out
		}
	}
	// fallback: cwd/out/sdk
	out := filepath.Join(cwd, "out", "sdk")
	must(os.MkdirAll(out, 0o755))
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
