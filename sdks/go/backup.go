// Package client: backup.go owns the SDK's whole-graph backup/restore
// surface (#685). Backup streams the LanternService BackupSnapshot RPC and
// serialises each frame to an io.Writer; Restore reads frames back and
// replays them through the batch Put RPCs. Two wire formats are supported:
// length-delimited protobuf (default, lossless) and newline-delimited JSON
// (human-readable). Restore reuses the existing PutVertices/PutEdges
// surface, so there is no dedicated restore RPC.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protodelim"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Format selects the on-disk encoding of a backup stream.
type Format int

const (
	// FormatProto is the default: a stream of length-delimited protobuf
	// BackupSnapshotResponse frames. Lossless and compact; not human
	// readable.
	FormatProto Format = iota
	// FormatNDJSON is one JSON object per line, discriminated by a "kind"
	// field ("vertex" | "edge"). Human-readable and greppable; values
	// round-trip via MarshalVertexJSON / MarshalEdgeJSON (int64/uint64
	// magnitudes survive through json.Number).
	FormatNDJSON
)

// defaultRestoreChunkSize is the batch size Restore sends per PutVertices /
// PutEdges call when WithRestoreChunkSize is not supplied.
const defaultRestoreChunkSize = 1000

// BackupStats / RestoreStats report how many vertices and edges were
// dumped or loaded.
type BackupStats struct {
	Vertices int
	Edges    int
}

type RestoreStats struct {
	Vertices int
	Edges    int
}

type backupConfig struct {
	format Format
	prefix string
}

// BackupOption customises Backup.
type BackupOption func(*backupConfig)

// WithBackupFormat selects the output format (default FormatProto).
func WithBackupFormat(f Format) BackupOption { return func(c *backupConfig) { c.format = f } }

// WithBackupPrefix restricts the backup to the induced subgraph over
// vertices whose key has this prefix (empty = the whole graph).
func WithBackupPrefix(p string) BackupOption { return func(c *backupConfig) { c.prefix = p } }

type restoreConfig struct {
	format    Format
	chunkSize int
}

// RestoreOption customises Restore.
type RestoreOption func(*restoreConfig)

// WithRestoreFormat selects the input format (default FormatProto). It must
// match the format the dump was written with.
func WithRestoreFormat(f Format) RestoreOption { return func(c *restoreConfig) { c.format = f } }

// WithRestoreChunkSize sets the batch size for the PutVertices / PutEdges
// calls Restore issues (non-positive values are ignored).
func WithRestoreChunkSize(n int) RestoreOption {
	return func(c *restoreConfig) {
		if n > 0 {
			c.chunkSize = n
		}
	}
}

// Backup streams a whole-graph, point-in-time dump to w. The server
// materialises the graph once via SnapshotGraph, so vertices and edges
// share one instant. Backup has no replication gate — it works against a
// single node. The returned stats count the vertices and edges written.
func (l *Lantern) Backup(ctx context.Context, w io.Writer, opts ...BackupOption) (BackupStats, error) {
	cfg := backupConfig{format: FormatProto}
	for _, o := range opts {
		o(&cfg)
	}

	stream, err := l.client.BackupSnapshot(ctx, connect.NewRequest(&pb.BackupSnapshotRequest{VertexPrefix: cfg.prefix}))
	if err != nil {
		return BackupStats{}, wrapConnectErr(err)
	}
	defer func() { _ = stream.Close() }()

	bw := bufio.NewWriter(w)
	var stats BackupStats
	for stream.Receive() {
		rec := stream.Msg()
		if err := encodeBackupRecord(bw, cfg.format, rec); err != nil {
			return stats, err
		}
		switch rec.GetRecord().(type) {
		case *pb.BackupSnapshotResponse_Vertex:
			stats.Vertices++
		case *pb.BackupSnapshotResponse_Edge:
			stats.Edges++
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return stats, wrapConnectErr(err)
	}
	if err := bw.Flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

// Restore reads a dump from r and replays it into the graph: vertices via
// PutVertices and edges via PutEdges (Put is idempotent, so re-running a
// restore is a no-op on unchanged data). Records are decoded verbatim into
// *Vertex / *Edge and put as-is, so value types and absolute expirations
// round-trip exactly. There is no rollback on a mid-restore failure
// (Lantern has no transactions). The returned stats count what was loaded.
func (l *Lantern) Restore(ctx context.Context, r io.Reader, opts ...RestoreOption) (RestoreStats, error) {
	cfg := restoreConfig{format: FormatProto, chunkSize: defaultRestoreChunkSize}
	for _, o := range opts {
		o(&cfg)
	}

	dec := newBackupDecoder(r, cfg.format)
	var stats RestoreStats
	vbatch := make([]*pb.Vertex, 0, cfg.chunkSize)
	ebatch := make([]*pb.Edge, 0, cfg.chunkSize)

	flushVertices := func() error {
		if len(vbatch) == 0 {
			return nil
		}
		resp, err := l.client.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vbatch}))
		if err != nil {
			return wrapConnectErr(err)
		}
		applied, err := appliedPutOutcomeCount(resp.Msg.GetOutcomes(), len(vbatch))
		if err != nil {
			return err
		}
		stats.Vertices += applied
		vbatch = vbatch[:0]
		return nil
	}
	flushEdges := func() error {
		if len(ebatch) == 0 {
			return nil
		}
		resp, err := l.client.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: ebatch}))
		if err != nil {
			return wrapConnectErr(err)
		}
		applied, err := appliedPutOutcomeCount(resp.Msg.GetOutcomes(), len(ebatch))
		if err != nil {
			return err
		}
		stats.Edges += applied
		ebatch = ebatch[:0]
		return nil
	}

	for {
		rec, err := dec.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stats, err
		}
		switch x := rec.GetRecord().(type) {
		case *pb.BackupSnapshotResponse_Vertex:
			vbatch = append(vbatch, x.Vertex)
			if len(vbatch) >= cfg.chunkSize {
				if err := flushVertices(); err != nil {
					return stats, err
				}
			}
		case *pb.BackupSnapshotResponse_Edge:
			ebatch = append(ebatch, x.Edge)
			if len(ebatch) >= cfg.chunkSize {
				if err := flushEdges(); err != nil {
					return stats, err
				}
			}
		}
	}
	// Vertices before edges: PutEdges auto-creates endpoints, but flushing
	// real vertex values first keeps endpoint values authoritative.
	if err := flushVertices(); err != nil {
		return stats, err
	}
	if err := flushEdges(); err != nil {
		return stats, err
	}
	return stats, nil
}

func appliedPutOutcomeCount(outcomes []pb.PutOutcome, want int) (int, error) {
	if len(outcomes) != want {
		return 0, fmt.Errorf("lantern: server returned %d Put outcomes for %d restore records", len(outcomes), want)
	}
	applied := 0
	for _, wire := range outcomes {
		outcome, err := putOutcomeFromProto(wire)
		if err != nil {
			return 0, err
		}
		if outcome == PutOutcomeAppliedAndLive {
			applied++
		}
	}
	return applied, nil
}

// encodeBackupRecord writes one frame in the selected format.
func encodeBackupRecord(w *bufio.Writer, f Format, rec *pb.BackupSnapshotResponse) error {
	if f == FormatNDJSON {
		line, err := marshalNDJSONRecord(rec)
		if err != nil {
			return err
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		return w.WriteByte('\n')
	}
	_, err := protodelim.MarshalTo(w, rec)
	return err
}

// marshalNDJSONRecord renders a backup frame as a single JSON object,
// splicing a "kind" discriminator onto MarshalVertexJSON / MarshalEdgeJSON
// output so the line is self-describing.
func marshalNDJSONRecord(rec *pb.BackupSnapshotResponse) ([]byte, error) {
	switch x := rec.GetRecord().(type) {
	case *pb.BackupSnapshotResponse_Vertex:
		b, err := MarshalVertexJSON(x.Vertex)
		if err != nil {
			return nil, err
		}
		return spliceKind("vertex", b), nil
	case *pb.BackupSnapshotResponse_Edge:
		b, err := MarshalEdgeJSON(x.Edge)
		if err != nil {
			return nil, err
		}
		return spliceKind("edge", b), nil
	default:
		return nil, errors.New("backup: record has no vertex or edge")
	}
}

// spliceKind inserts {"kind":"<kind>", ...} as the first member of a JSON
// object produced by Marshal*JSON.
func spliceKind(kind string, obj []byte) []byte {
	if len(obj) <= 2 { // "{}" — no other members
		return []byte(`{"kind":"` + kind + `"}`)
	}
	prefix := []byte(`{"kind":"` + kind + `",`)
	return append(prefix, obj[1:]...)
}

// backupDecoder yields successive frames from a dump, hiding the
// format-specific framing behind next().
type backupDecoder struct {
	format Format
	br     *bufio.Reader
	sc     *bufio.Scanner
	lineNo int
}

func newBackupDecoder(r io.Reader, f Format) *backupDecoder {
	d := &backupDecoder{format: f}
	if f == FormatNDJSON {
		d.sc = bufio.NewScanner(r)
		// Match the bulk loader's generous line cap so large base64/string
		// values don't trip bufio.Scanner's default 64 KiB token limit.
		d.sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	} else {
		d.br = bufio.NewReader(r)
	}
	return d
}

// next returns the next frame, or io.EOF at a clean end of stream.
func (d *backupDecoder) next() (*pb.BackupSnapshotResponse, error) {
	if d.format == FormatNDJSON {
		for d.sc.Scan() {
			d.lineNo++
			if len(d.sc.Bytes()) == 0 {
				continue
			}
			rec, err := parseNDJSONRecord(d.sc.Bytes())
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", d.lineNo, err)
			}
			return rec, nil
		}
		if err := d.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	var rec pb.BackupSnapshotResponse
	if err := protodelim.UnmarshalFrom(d.br, &rec); err != nil {
		return nil, err // io.EOF at a clean end
	}
	return &rec, nil
}

// parseNDJSONRecord routes one JSON line by its "kind" discriminator,
// reusing UnmarshalVertexJSON / UnmarshalEdgeJSON (both ignore the extra
// "kind" field).
func parseNDJSONRecord(line []byte) (*pb.BackupSnapshotResponse, error) {
	var disc struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(line, &disc); err != nil {
		return nil, err
	}
	switch disc.Kind {
	case "vertex":
		v, err := UnmarshalVertexJSON(line)
		if err != nil {
			return nil, err
		}
		return &pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Vertex{Vertex: v}}, nil
	case "edge":
		e, err := UnmarshalEdgeJSON(line)
		if err != nil {
			return nil, err
		}
		return &pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Edge{Edge: e}}, nil
	default:
		return nil, fmt.Errorf("unknown or missing kind %q", disc.Kind)
	}
}
