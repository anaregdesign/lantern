package backup

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestReceiptBaselineCaptureStagingOwnsInputs(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	capture := producerCapture(archive)
	capture.Retired = producerRetiredSnapshot(t, archive.Policy, archive.Receipts.ClockHighWaterMillis, 0x72)
	wantGraph := proto.CloneOf(capture.Graph[1].GetVertex().GetVertex())
	wantReceipt := bytes.Clone(capture.Receipts.Receipts[0].Result)
	wantRetired := bytes.Clone(capture.Retired.Epochs[0].State.Receipts[0].Result)
	codec := ReceiptBaselineCodec{}
	staged, frames, err := codec.StageReceiptBaselineCapture(t.Context(), capture, archive.Policy, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if staged.CutoffLocalSeq != archive.Graph[0].GetHeader().GetCutoffLocalSeq() ||
		staged.CutoffHLC != archive.Origins[0].LastHLC ||
		!reflect.DeepEqual(staged.Origins, archive.Origins) ||
		len(frames) != len(capture.Graph) {
		t.Fatal("direct staging changed baseline provenance")
	}
	for i := range frames {
		if !proto.Equal(frames[i], capture.Graph[i]) {
			t.Fatalf("canonical graph frame %d changed", i)
		}
	}
	capture.Graph[1].GetVertex().GetVertex().Key = "mutated"
	capture.Receipts.Receipts[0].Result[0] ^= 1
	capture.Retired.Epochs[0].State.Receipts[0].Result[0] ^= 1
	capture.Origins[0].LastSeq++
	if got, ok := staged.Graph.GetVertex(wantGraph.Key); !ok || !proto.Equal(got, wantGraph) ||
		!proto.Equal(frames[1].GetVertex().GetVertex(), wantGraph) {
		t.Fatal("staged graph or publication frames retained caller ownership")
	}
	receipts, err := staged.Receipts.Snapshot()
	if err != nil || !bytes.Equal(receipts.Receipts[0].Result, wantReceipt) {
		t.Fatalf("staged active receipts changed: %v, %v", receipts, err)
	}
	if !bytes.Equal(staged.Retired.Epochs[0].State.Receipts[0].Result, wantRetired) ||
		staged.Origins[0].LastSeq == capture.Origins[0].LastSeq {
		t.Fatal("staged retired receipts or origins retained caller ownership")
	}
}

func TestReceiptBaselineCaptureStagingRejectsMalformedCuts(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	codec := ReceiptBaselineCodec{}
	t.Run("missing footer", func(t *testing.T) {
		capture := producerCapture(archive)
		capture.Graph = capture.Graph[:len(capture.Graph)-1]
		if result, frames, err := codec.StageReceiptBaselineCapture(t.Context(), capture, archive.Policy, time.Hour, nil); result != nil || frames != nil || !errors.Is(err, errWholeStateArchive) {
			t.Fatalf("partial graph staged: %v, %v, %v", result, frames, err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		capture := producerCapture(archive)
		if result, frames, err := codec.StageReceiptBaselineCapture(ctx, capture, archive.Policy, time.Hour, nil); result != nil || frames != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled baseline staged: %v, %v, %v", result, frames, err)
		}
		if digest, size, err := codec.DigestCombinedReceiptBaseline(ctx, capture); digest != [32]byte{} || size != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled baseline published a digest: %x, %d, %v", digest, size, err)
		}
	})
}
