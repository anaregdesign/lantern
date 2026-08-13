package client

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func mkVertexRec(v *pb.Vertex) *pb.BackupSnapshotResponse {
	return &pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Vertex{Vertex: v}}
}

func mkEdgeRec(e *pb.Edge) *pb.BackupSnapshotResponse {
	return &pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Edge{Edge: e}}
}

func sampleBackupRecords() []*pb.BackupSnapshotResponse {
	exp := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	return []*pb.BackupSnapshotResponse{
		mkVertexRec(&pb.Vertex{Key: "alice", Value: &pb.Vertex_String_{String_: "Alice"}}),
		mkVertexRec(&pb.Vertex{Key: "big", Value: &pb.Vertex_Int64{Int64: math.MaxInt64}}),
		mkVertexRec(&pb.Vertex{Key: "ubig", Value: &pb.Vertex_Uint64{Uint64: math.MaxUint64}}),
		mkVertexRec(&pb.Vertex{Key: "raw", Value: &pb.Vertex_Bytes{Bytes: []byte{0x00, 0xff, 0x10}}}),
		mkVertexRec(&pb.Vertex{Key: "ttl", Expiration: timestamppb.New(exp), Value: &pb.Vertex_Float64{Float64: 2.5}}),
		mkEdgeRec(&pb.Edge{Tail: "alice", Head: "big", Weight: 1.5}),
		mkEdgeRec(&pb.Edge{Tail: "big", Head: "ubig", Weight: 2, Expiration: timestamppb.New(exp)}),
	}
}

// TestBackupCodec_RoundTrip encodes a mixed vertex/edge stream and decodes
// it back in both formats, asserting exact equality (the format-orchestration
// half of #692, exercised without a server).
func TestBackupCodec_RoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format Format
	}{
		{"proto", FormatProto},
		{"ndjson", FormatNDJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recs := sampleBackupRecords()

			var buf bytes.Buffer
			bw := bufio.NewWriter(&buf)
			for _, r := range recs {
				if err := encodeBackupRecord(bw, tc.format, r); err != nil {
					t.Fatalf("encode: %v", err)
				}
			}
			if err := bw.Flush(); err != nil {
				t.Fatalf("flush: %v", err)
			}

			dec := newBackupDecoder(&buf, tc.format)
			var got []*pb.BackupSnapshotResponse
			for {
				rec, err := dec.next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				got = append(got, rec)
			}

			if len(got) != len(recs) {
				t.Fatalf("decoded %d records, want %d", len(got), len(recs))
			}
			for i := range recs {
				if !proto.Equal(got[i], recs[i]) {
					t.Errorf("[%d] round-trip mismatch:\n got=%v\nwant=%v", i, got[i], recs[i])
				}
			}
		})
	}
}

func TestBackupCodec_NDJSONUnknownKind(t *testing.T) {
	dec := newBackupDecoder(strings.NewReader(`{"kind":"bogus","key":"x"}`+"\n"), FormatNDJSON)
	if _, err := dec.next(); err == nil {
		t.Fatal("want error for unknown kind, got nil")
	}
}

func TestAppliedPutOutcomeCount(t *testing.T) {
	got, err := appliedPutOutcomeCount([]pb.PutOutcome{
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_EXPIRED,
		pb.PutOutcome_PUT_OUTCOME_SUPERSEDED,
	}, 3)
	if err != nil || got != 1 {
		t.Fatalf("appliedPutOutcomeCount = (%d, %v), want (1, nil)", got, err)
	}
	if _, err := appliedPutOutcomeCount(nil, 1); err == nil {
		t.Fatal("appliedPutOutcomeCount accepted a length mismatch")
	}
	if _, err := appliedPutOutcomeCount([]pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_UNSPECIFIED}, 1); err == nil {
		t.Fatal("appliedPutOutcomeCount accepted UNSPECIFIED")
	}
}

// TestBackupCodec_NDJSONShape locks the on-disk NDJSON line shape: a "kind"
// discriminator spliced ahead of the MarshalVertexJSON / MarshalEdgeJSON
// fields.
func TestBackupCodec_NDJSONShape(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := encodeBackupRecord(bw, FormatNDJSON, mkVertexRec(&pb.Vertex{Key: "k", Value: &pb.Vertex_String_{String_: "v"}})); err != nil {
		t.Fatal(err)
	}
	if err := encodeBackupRecord(bw, FormatNDJSON, mkEdgeRec(&pb.Edge{Tail: "a", Head: "b", Weight: 1})); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], `{"kind":"vertex",`) {
		t.Errorf("vertex line = %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], `{"kind":"edge",`) {
		t.Errorf("edge line = %s", lines[1])
	}
}
