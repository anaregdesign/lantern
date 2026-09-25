package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type receiptSnapshotTestStream struct {
	frames  []*pb.SnapshotResponse
	raw     [][]byte
	next    int
	current int
	err     error
	before  func(int)
}

type receiptSnapshotDecodedTestStream struct {
	stream *receiptSnapshotTestStream
}

func (s *receiptSnapshotDecodedTestStream) Receive() bool {
	return s.stream.Receive()
}

func (s *receiptSnapshotDecodedTestStream) Msg() *pb.SnapshotResponse {
	return s.stream.Msg()
}

func (s *receiptSnapshotDecodedTestStream) Err() error {
	return s.stream.Err()
}

func (s *receiptSnapshotTestStream) Receive() bool {
	if s.before != nil {
		s.before(s.next)
	}
	if s.next >= len(s.frames) {
		return false
	}
	s.current = s.next
	s.next++
	return true
}

func (s *receiptSnapshotTestStream) Msg() *pb.SnapshotResponse {
	if s.current < 0 || s.current >= len(s.frames) {
		return nil
	}
	return s.frames[s.current]
}

func (s *receiptSnapshotTestStream) Err() error {
	if s.next < len(s.frames) {
		return nil
	}
	return s.err
}

func (s *receiptSnapshotTestStream) ReceiptSnapshotWireBytes() []byte {
	if s.current < 0 || s.current >= len(s.frames) {
		return nil
	}
	if s.current >= len(s.raw) {
		raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(s.frames[s.current])
		return raw
	}
	return s.raw[s.current]
}

func receiptSnapshotCollectorFixture(
	t *testing.T,
) ([]*pb.SnapshotResponse, mutationreceipt.Config) {
	t.Helper()
	issued := time.Now().Add(-time.Minute).UTC()
	policy := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x31}, Retention: time.Hour,
		MaxEntries: 16, MaxBytes: 1 << 20,
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	id, err := mutationreceipt.NewID(policy.Epoch, issued, [24]byte{0x32})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID: id, Group: mutationreceipt.GroupID{0x33}, Count: 1,
		Kind: mutationreceipt.AddEdge, Digest: mutationreceipt.IntentDigest([]byte("collector")),
		HasContrib: true, ContribID: mutationreceipt.ContribID{0x34},
	}
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if class, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil ||
		class != mutationreceipt.Fresh {
		t.Fatalf("Classify = (%v, %v)", class, err)
	}
	if err := tx.Reserve([][]byte{{0xde, 0xad}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	origin := hlc.NodeID{0x35}
	stamp := hlc.Timestamp{
		WallNs:  time.Now().Add(time.Minute).UnixNano(),
		Logical: 2,
		NodeID:  origin,
	}
	wireHLC := func() *pb.HLCTimestamp {
		return &pb.HLCTimestamp{
			WallNs: stamp.WallNs, Logical: stamp.Logical,
			NodeId: append([]byte(nil), stamp.NodeID[:]...),
		}
	}
	fingerprint := store.PolicyFingerprint()
	return []*pb.SnapshotResponse{
		{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
			CutoffSeqPerOrigin: map[string]uint64{"35000000000000000000000000000000": 7},
			CutoffHlc:          wireHLC(),
			CutoffLocalSeq:     11,
			Format:             pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1,
			ReceiptMetadata: &pb.SnapshotReceiptMetadata{
				Policy: &pb.ReceiptPolicy{
					DeploymentEpoch: append([]byte(nil), policy.Epoch[:]...),
					Fingerprint:     append([]byte(nil), fingerprint[:]...),
					RetentionMs:     uint64(policy.Retention / time.Millisecond),
					MaxEntries:      uint64(policy.MaxEntries),
					MaxBytes:        policy.MaxBytes,
				},
				ClockHighWaterUnixMs: uint64(state.ClockHighWaterMillis),
				OriginCutoffs: []*pb.OriginState{{
					Origin:  append([]byte(nil), origin[:]...),
					LastSeq: 7,
					LastHlc: wireHLC(),
				}},
			},
		}}},
		{Entry: &pb.SnapshotResponse_Receipt{Receipt: &pb.SnapshotReceipt{
			OperationId:    state.Receipts[0].ID.Bytes(),
			LogicalCallId:  append([]byte(nil), state.Receipts[0].Group[:]...),
			ItemIndex:      state.Receipts[0].Index,
			ItemCount:      state.Receipts[0].Count,
			Kind:           pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_ADD_EDGE,
			IntentSha256:   append([]byte(nil), state.Receipts[0].Digest[:]...),
			DeadlineUnixMs: uint64(state.Receipts[0].DeadlineMillis),
			OriginalResult: append([]byte(nil), state.Receipts[0].Result...),
			Contribution: &pb.SnapshotReceiptContribution{
				ContributionId: append([]byte(nil), state.Receipts[0].ContribID[:]...),
			},
		}}},
		{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{
			Vertex: &pb.Vertex{Key: "tail"}, Hlc: wireHLC(),
		}}},
		{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{
			Vertex: &pb.Vertex{Key: "head"}, Hlc: wireHLC(),
		}}},
		{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{
			Tail: "tail", Head: "head",
			Contributions: []*pb.SnapshotEdgeContribution{{
				Weight: 1.5, ContribId: append([]byte(nil), intent.ContribID[:]...),
				Hlc: wireHLC(),
			}},
		}}},
		{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{
			VertexCount: 2, EdgeCount: 1, ReceiptCount: 1, ReceiptOriginCount: 1,
		}}},
	}, policy
}

func cloneReceiptSnapshotCollectorFrames(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
	cloned := make([]*pb.SnapshotResponse, len(frames))
	for i, frame := range frames {
		if frame != nil {
			cloned[i] = proto.Clone(frame).(*pb.SnapshotResponse)
		}
	}
	return cloned
}

func receiptSnapshotCollectorLimits() ReceiptSnapshotCollectorLimits {
	return ReceiptSnapshotCollectorLimits{
		MaxFrameBytes:  1 << 20,
		MaxFrames:      32,
		MaxTotalBytes:  4 << 20,
		MaxReceipts:    8,
		MaxOrigins:     8,
		MaxGraphFrames: 16,
	}
}

func newReceiptSnapshotTestCollector(
	t *testing.T,
	dir string,
	policy mutationreceipt.Config,
	limits ReceiptSnapshotCollectorLimits,
) *ReceiptSnapshotCollector {
	t.Helper()
	collector, err := NewReceiptSnapshotCollector(ReceiptSnapshotCollectorConfig{
		TempDir: dir, Limits: limits, ExpectedPolicy: policy, DefaultTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return collector
}

func assertReceiptSnapshotTempDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("receipt Snapshot temporary artifacts remain: %+v", entries)
	}
}

func TestReceiptSnapshotCollectorReturnsCanonicalDetachedCandidate(t *testing.T) {
	frames, policy := receiptSnapshotCollectorFixture(t)
	dir := t.TempDir()
	collector := newReceiptSnapshotTestCollector(
		t, dir, policy, receiptSnapshotCollectorLimits(),
	)
	candidate, err := collector.Collect(
		context.Background(),
		&receiptSnapshotTestStream{frames: frames, current: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := candidate.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	metadata := candidate.Metadata()
	if metadata.Header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		metadata.Header.GetCutoffLocalSeq() != 11 ||
		len(metadata.Header.GetReceiptMetadata().GetOriginCutoffs()) != 1 ||
		metadata.Policy.Epoch != policy.Epoch ||
		metadata.ArchiveBytes == 0 ||
		metadata.ArchiveSHA256 == ([sha256.Size]byte{}) {
		t.Fatalf("candidate metadata = %+v", metadata)
	}
	metadata.Header.CutoffLocalSeq++
	if candidate.Metadata().Header.GetCutoffLocalSeq() != 11 {
		t.Fatal("candidate metadata header aliases caller")
	}
	var first, second bytes.Buffer
	if err := candidate.WriteArchive(&first); err != nil {
		t.Fatal(err)
	}
	if err := candidate.WriteArchive(&second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) ||
		sha256.Sum256(first.Bytes()) != candidate.Metadata().ArchiveSHA256 {
		t.Fatal("candidate archive is not deterministic or digest-bound")
	}
	archive, err := decodeWholeStateArchive(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Receipts.Receipts) != 1 || len(archive.Origins) != 1 ||
		len(archive.Graph) != 5 ||
		archive.Graph[0].GetHeader().GetReceiptMetadata() != nil ||
		archive.Graph[len(archive.Graph)-1].GetFooter().GetReceiptCount() != 0 {
		t.Fatalf("canonical archive lost split state: %+v", archive)
	}
	if candidate.stage == nil || candidate.stage.graph == nil ||
		candidate.stage.receipts == nil ||
		candidate.stage.cutoffLocalSeq != 11 ||
		len(candidate.stage.origins) != 1 {
		t.Fatalf("detached stage = %+v", candidate.stage)
	}
	if weight, _, ok := candidate.stage.graph.GetEdgeDetail("tail", "head"); !ok || weight != 1.5 {
		t.Fatalf("staged graph edge = %v, %t", weight, ok)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("successful candidate temp files = %v, %v", entries, err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	assertReceiptSnapshotTempDirEmpty(t, dir)
	if err := candidate.WriteArchive(io.Discard); err == nil {
		t.Fatal("closed candidate remained readable")
	}
}

func TestReceiptSnapshotCollectorCanonicalizesDecodedTransportMessages(t *testing.T) {
	frames, policy := receiptSnapshotCollectorFixture(t)
	dir := t.TempDir()
	collector := newReceiptSnapshotTestCollector(
		t, dir, policy, receiptSnapshotCollectorLimits(),
	)
	candidate, err := collector.Collect(
		context.Background(),
		&receiptSnapshotDecodedTestStream{stream: &receiptSnapshotTestStream{
			frames: cloneReceiptSnapshotCollectorFrames(frames), current: -1,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Close()
	var archive bytes.Buffer
	if err := candidate.WriteArchive(&archive); err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(archive.Bytes()) != candidate.Metadata().ArchiveSHA256 {
		t.Fatal("decoded transport did not produce a digest-bound canonical archive")
	}
}

func TestReceiptSnapshotCollectorRejectsMalformedStreamsWithoutArtifacts(t *testing.T) {
	valid, policy := receiptSnapshotCollectorFixture(t)
	streamFailure := errors.New("injected receive failure")
	tests := []struct {
		name   string
		mutate func([]*pb.SnapshotResponse) []*pb.SnapshotResponse
		err    error
		policy func(mutationreceipt.Config) mutationreceipt.Config
	}{
		{"empty", func([]*pb.SnapshotResponse) []*pb.SnapshotResponse { return nil }, nil, nil},
		{"body before header", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			return frames[1:]
		}, nil, nil},
		{"missing footer", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			return frames[:len(frames)-1]
		}, nil, nil},
		{"duplicate header", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			return append(frames[:1], append([]*pb.SnapshotResponse{
				proto.Clone(frames[0]).(*pb.SnapshotResponse),
			}, frames[1:]...)...)
		}, nil, nil},
		{"duplicate footer", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			return append(frames, proto.Clone(frames[len(frames)-1]).(*pb.SnapshotResponse))
		}, nil, nil},
		{"trailing body", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			return append(frames, proto.Clone(frames[2]).(*pb.SnapshotResponse))
		}, nil, nil},
		{"format downgrade", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().Format = pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
			return frames
		}, nil, nil},
		{"missing receipt metadata", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().ReceiptMetadata = nil
			return frames
		}, nil, nil},
		{"receipt after graph", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[1], frames[2] = frames[2], frames[1]
			return frames
		}, nil, nil},
		{"footer count drift", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[len(frames)-1].GetFooter().EdgeCount++
			return frames
		}, nil, nil},
		{"policy fingerprint drift", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().GetReceiptMetadata().GetPolicy().Fingerprint[0] ^= 1
			return frames
		}, nil, nil},
		{"expected epoch mismatch", nil, nil, func(config mutationreceipt.Config) mutationreceipt.Config {
			config.Epoch[0] ^= 1
			return config
		}},
		{"expected policy mismatch", nil, nil, func(config mutationreceipt.Config) mutationreceipt.Config {
			config.MaxBytes++
			return config
		}},
		{"origin cutoff drift", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().GetReceiptMetadata().GetOriginCutoffs()[0].LastSeq++
			return frames
		}, nil, nil},
		{"unknown graph origin", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetHlc().NodeId[0] ^= 1
			return frames
		}, nil, nil},
		{"receipt digest length", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[1].GetReceipt().IntentSha256 = []byte{1}
			return frames
		}, nil, nil},
		{"unknown field", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			return frames
		}, nil, nil},
		{"typed nil frame", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].Entry = (*pb.SnapshotResponse_Vertex)(nil)
			return frames
		}, nil, nil},
		{"typed nil vertex value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_String_)(nil)
			return frames
		}, nil, nil},
		{"stream error", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			return frames[:2]
		}, streamFailure, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			frames := cloneReceiptSnapshotCollectorFrames(valid)
			if test.mutate != nil {
				frames = test.mutate(frames)
			}
			expected := policy
			if test.policy != nil {
				expected = test.policy(expected)
			}
			dir := t.TempDir()
			collector := newReceiptSnapshotTestCollector(
				t, dir, expected, receiptSnapshotCollectorLimits(),
			)
			candidate, err := collector.Collect(context.Background(), &receiptSnapshotTestStream{
				frames: frames, current: -1, err: test.err,
			})
			if err == nil || candidate != nil {
				if candidate != nil {
					_ = candidate.Close()
				}
				t.Fatalf("malformed stream returned candidate=%p, err=%v", candidate, err)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("stream error = %v, want %v", err, test.err)
			}
			assertReceiptSnapshotTempDirEmpty(t, dir)
		})
	}
}

func TestReceiptSnapshotCollectorRejectsWireAmbiguity(t *testing.T) {
	valid, policy := receiptSnapshotCollectorFixture(t)
	marshal := proto.MarshalOptions{Deterministic: true}
	raw := make([][]byte, len(valid))
	for i, frame := range valid {
		var err error
		raw[i], err = marshal.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		mutate func([][]byte)
	}{
		{"duplicate oneof field", func(frames [][]byte) {
			frames[0] = append(bytes.Clone(frames[0]), frames[0]...)
		}},
		{"nonminimal field tag", func(frames [][]byte) {
			frames[0] = append([]byte{0x8a, 0x00}, frames[0][1:]...)
		}},
		{"wire message mismatch", func(frames [][]byte) {
			frames[0] = bytes.Clone(frames[0])
			frames[0][len(frames[0])-1] ^= 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exact := make([][]byte, len(raw))
			for i := range raw {
				exact[i] = bytes.Clone(raw[i])
			}
			test.mutate(exact)
			dir := t.TempDir()
			collector := newReceiptSnapshotTestCollector(
				t, dir, policy, receiptSnapshotCollectorLimits(),
			)
			candidate, err := collector.Collect(context.Background(), &receiptSnapshotTestStream{
				frames:  cloneReceiptSnapshotCollectorFrames(valid),
				raw:     exact,
				current: -1,
			})
			if err == nil || candidate != nil {
				if candidate != nil {
					_ = candidate.Close()
				}
				t.Fatalf("ambiguous wire returned candidate=%p, err=%v", candidate, err)
			}
			assertReceiptSnapshotTempDirEmpty(t, dir)
		})
	}
}

func TestReceiptSnapshotCollectorEnforcesEveryBound(t *testing.T) {
	valid, policy := receiptSnapshotCollectorFixture(t)
	tests := []struct {
		name   string
		frames func() []*pb.SnapshotResponse
		limits func(ReceiptSnapshotCollectorLimits) ReceiptSnapshotCollectorLimits
	}{
		{"frame bytes", func() []*pb.SnapshotResponse { return cloneReceiptSnapshotCollectorFrames(valid) }, func(l ReceiptSnapshotCollectorLimits) ReceiptSnapshotCollectorLimits {
			l.MaxFrameBytes = 8
			return l
		}},
		{"frame count", func() []*pb.SnapshotResponse { return cloneReceiptSnapshotCollectorFrames(valid) }, func(l ReceiptSnapshotCollectorLimits) ReceiptSnapshotCollectorLimits {
			l.MaxFrames = uint64(len(valid) - 1)
			l.MaxReceipts = l.MaxFrames
			l.MaxGraphFrames = l.MaxFrames
			return l
		}},
		{"total bytes", func() []*pb.SnapshotResponse { return cloneReceiptSnapshotCollectorFrames(valid) }, func(l ReceiptSnapshotCollectorLimits) ReceiptSnapshotCollectorLimits {
			l.MaxTotalBytes = 64
			return l
		}},
		{"receipt count", func() []*pb.SnapshotResponse {
			frames := cloneReceiptSnapshotCollectorFrames(valid)
			frames = append(frames[:2], append([]*pb.SnapshotResponse{
				proto.Clone(frames[1]).(*pb.SnapshotResponse),
			}, frames[2:]...)...)
			frames[len(frames)-1].GetFooter().ReceiptCount++
			return frames
		}, func(l ReceiptSnapshotCollectorLimits) ReceiptSnapshotCollectorLimits {
			l.MaxReceipts = 1
			return l
		}},
		{"origin count", func() []*pb.SnapshotResponse {
			frames := cloneReceiptSnapshotCollectorFrames(valid)
			metadata := frames[0].GetHeader().GetReceiptMetadata()
			second := proto.Clone(metadata.GetOriginCutoffs()[0]).(*pb.OriginState)
			second.Origin[0]++
			second.LastHlc.NodeId[0]++
			metadata.OriginCutoffs = append(metadata.OriginCutoffs, second)
			return frames
		}, func(l ReceiptSnapshotCollectorLimits) ReceiptSnapshotCollectorLimits {
			l.MaxOrigins = 1
			return l
		}},
		{"graph count", func() []*pb.SnapshotResponse { return cloneReceiptSnapshotCollectorFrames(valid) }, func(l ReceiptSnapshotCollectorLimits) ReceiptSnapshotCollectorLimits {
			l.MaxGraphFrames = 2
			return l
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			collector := newReceiptSnapshotTestCollector(
				t, dir, policy, test.limits(receiptSnapshotCollectorLimits()),
			)
			candidate, err := collector.Collect(context.Background(), &receiptSnapshotTestStream{
				frames: test.frames(), current: -1,
			})
			if err == nil || candidate != nil {
				if candidate != nil {
					_ = candidate.Close()
				}
				t.Fatalf("limit breach returned candidate=%p, err=%v", candidate, err)
			}
			assertReceiptSnapshotTempDirEmpty(t, dir)
		})
	}
}

func TestReceiptSnapshotCollectorCancellationCleansEveryPhase(t *testing.T) {
	valid, policy := receiptSnapshotCollectorFixture(t)
	for cancelAt := 0; cancelAt <= len(valid); cancelAt++ {
		t.Run("receive-"+strings.Repeat("x", cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			dir := t.TempDir()
			collector := newReceiptSnapshotTestCollector(
				t, dir, policy, receiptSnapshotCollectorLimits(),
			)
			stream := &receiptSnapshotTestStream{
				frames:  cloneReceiptSnapshotCollectorFrames(valid),
				current: -1,
				before: func(index int) {
					if index == cancelAt {
						cancel()
					}
				},
			}
			candidate, err := collector.Collect(ctx, stream)
			if candidate != nil || !errors.Is(err, context.Canceled) {
				if candidate != nil {
					_ = candidate.Close()
				}
				t.Fatalf("cancellation returned candidate=%p, err=%v", candidate, err)
			}
			assertReceiptSnapshotTempDirEmpty(t, dir)
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	collector, err := NewReceiptSnapshotCollector(ReceiptSnapshotCollectorConfig{
		TempDir: dir, Limits: receiptSnapshotCollectorLimits(),
		ExpectedPolicy: policy, DefaultTTL: time.Hour,
		ConfigureGraph: func(*graphcache.GraphCache[string, *pb.Vertex]) error {
			cancel()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := collector.Collect(ctx, &receiptSnapshotTestStream{
		frames: cloneReceiptSnapshotCollectorFrames(valid), current: -1,
	})
	if candidate != nil || !errors.Is(err, context.Canceled) {
		if candidate != nil {
			_ = candidate.Close()
		}
		t.Fatalf("stage cancellation returned candidate=%p, err=%v", candidate, err)
	}
	assertReceiptSnapshotTempDirEmpty(t, dir)
}

func TestNewReceiptSnapshotCollectorRejectsUnboundedConfiguration(t *testing.T) {
	_, policy := receiptSnapshotCollectorFixture(t)
	valid := ReceiptSnapshotCollectorConfig{
		TempDir: t.TempDir(), Limits: receiptSnapshotCollectorLimits(),
		ExpectedPolicy: policy, DefaultTTL: time.Hour,
	}
	tests := []struct {
		name   string
		mutate func(*ReceiptSnapshotCollectorConfig)
	}{
		{"temporary directory", func(c *ReceiptSnapshotCollectorConfig) { c.TempDir = "" }},
		{"frame bytes", func(c *ReceiptSnapshotCollectorConfig) { c.Limits.MaxFrameBytes = 0 }},
		{"frame count", func(c *ReceiptSnapshotCollectorConfig) { c.Limits.MaxFrames = 1 }},
		{"total bytes", func(c *ReceiptSnapshotCollectorConfig) { c.Limits.MaxTotalBytes = 0 }},
		{"receipts", func(c *ReceiptSnapshotCollectorConfig) { c.Limits.MaxReceipts = 0 }},
		{"origins", func(c *ReceiptSnapshotCollectorConfig) { c.Limits.MaxOrigins = 0 }},
		{"graph frames", func(c *ReceiptSnapshotCollectorConfig) { c.Limits.MaxGraphFrames = 0 }},
		{"policy", func(c *ReceiptSnapshotCollectorConfig) { c.ExpectedPolicy.MaxBytes = 0 }},
		{"default TTL", func(c *ReceiptSnapshotCollectorConfig) { c.DefaultTTL = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if collector, err := NewReceiptSnapshotCollector(config); err == nil || collector != nil {
				t.Fatalf("invalid config returned collector=%p, err=%v", collector, err)
			}
		})
	}
}

func TestReceiptSnapshotCandidateMetadataAndArchiveAreOwned(t *testing.T) {
	frames, policy := receiptSnapshotCollectorFixture(t)
	dir := t.TempDir()
	collector := newReceiptSnapshotTestCollector(
		t, dir, policy, receiptSnapshotCollectorLimits(),
	)
	candidate, err := collector.Collect(context.Background(), &receiptSnapshotTestStream{
		frames: cloneReceiptSnapshotCollectorFrames(frames), current: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Close()
	before := candidate.Metadata()
	frames[0].GetHeader().CutoffLocalSeq++
	frames[1].GetReceipt().OriginalResult[0] ^= 1
	after := candidate.Metadata()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("candidate metadata aliased stream: before=%+v after=%+v", before, after)
	}
}
