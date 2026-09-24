package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

// produceReceiptWholeStateArchive is an unwired in-process prerequisite for a
// receipt-aware backup. Its sole source call returns a detached publication
// cut; every archive section comes from that value, never a later live read.
// The complete bytes are decoded before return so capture, policy, size, and
// codec failures cannot yield a partial archive. A checksum and a coherent
// in-process cut do not prove a durable WAL frontier or authorize restore.
func produceReceiptWholeStateArchive(ctx context.Context, source service.ReceiptWholeStateSource, policy mutationreceipt.Config) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil {
		return nil, errors.New("backup: receipt whole-state source is nil")
	}
	capture, err := source(ctx, policy)
	if err != nil {
		return nil, fmt.Errorf("backup: capture receipt whole-state: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if capture.Policy.Epoch != policy.Epoch || capture.Policy.Retention != policy.Retention ||
		capture.Policy.MaxEntries != policy.MaxEntries || capture.Policy.MaxBytes != policy.MaxBytes ||
		(!policy.ClockHighWater.IsZero() && policy.ClockHighWater.UnixMilli() > capture.Policy.ClockHighWater.UnixMilli()) {
		return nil, wholeStateArchiveError("capture policy differs from requested policy")
	}
	archive := wholeStateArchive{
		Graph: capture.Graph, Receipts: capture.Receipts,
		Policy: capture.Policy, Origins: capture.Origins,
	}
	var out bytes.Buffer
	if err := encodeWholeStateArchive(&out, archive); err != nil {
		return nil, err
	}
	if _, err := decodeWholeStateArchive(bytes.NewReader(out.Bytes())); err != nil {
		return nil, fmt.Errorf("backup: verify receipt whole-state archive: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
