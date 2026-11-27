package dataruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/tarp"
)

// Search source rows live in the authoritative Graph commit. The accepted
// sequence is independent of local relay sequence and storage generations.
const (
	tarpSearchSource       byte = 24
	tarpSearchChange       byte = 25
	searchChangeChunkBytes      = 64 << 10
)

type tarpSearchCut struct {
	epoch  uint64
	last   uint64
	first  uint64
	digest [sha256.Size]byte
}

var errTarpSearchCut = errors.New("invalid Tarp Search source cut")

func (c tarpSearchCut) encode() []byte {
	b := binary.BigEndian.AppendUint64([]byte("LNS1"), c.epoch)
	b = binary.BigEndian.AppendUint64(b, c.last)
	b = binary.BigEndian.AppendUint64(b, c.first)
	return append(b, c.digest[:]...)
}

func readTarpSearchCut(ctx context.Context, snap *tarp.Snapshot, gen uint64) (tarpSearchCut, error) {
	var c tarpSearchCut
	b, err := snap.GetContext(ctx, tarpServiceKey(gen, tarpSearchSource))
	if err != nil {
		return c, err
	}
	if len(b) != 28+sha256.Size || string(b[:4]) != "LNS1" {
		return c, errTarpSearchCut
	}
	c.epoch = binary.BigEndian.Uint64(b[4:12])
	c.last = binary.BigEndian.Uint64(b[12:20])
	c.first = binary.BigEndian.Uint64(b[20:28])
	copy(c.digest[:], b[28:])
	if c.epoch == 0 || c.last == math.MaxUint64 || c.first == 0 || c.first > c.last+1 {
		return tarpSearchCut{}, errTarpSearchCut
	}
	return c, nil
}

func tarpSearchSequencePrefix(gen, seq uint64) []byte {
	return binary.BigEndian.AppendUint64(tarpServiceKey(gen, tarpSearchChange), seq)
}

func tarpSearchChangeKey(gen, seq uint64, chunk uint32) []byte {
	return binary.BigEndian.AppendUint32(tarpSearchSequencePrefix(gen, seq), chunk)
}

// stageTarpSearchChanges records the actual changed Vertex identities and the
// authenticated graph publication identity in the caller's existing atomic
// batch. A physical generation replacement copies these service rows; a true
// Graph replacement installs a new epoch. Keys retain their physical namespace.
func stageTarpSearchChanges(b graphstore.Writer, gen uint64, cut tarpSearchCut, publication [sha256.Size]byte, keys []string) (tarpSearchCut, error) {
	if cut.epoch == 0 || cut.last >= math.MaxUint64-1 || cut.first == 0 || cut.first > cut.last+1 {
		return cut, errTarpSearchCut
	}
	if len(keys) == 0 {
		return cut, nil
	}
	keys = slices.Clone(keys)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	for _, key := range keys {
		if key == "" || len(key) > searchChangeChunkBytes-binary.MaxVarintLen64 {
			return cut, fmt.Errorf("%w: invalid Vertex identity", errTarpSearchCut)
		}
	}

	cut.last++
	prefix := tarpSearchSequencePrefix(gen, cut.last)
	h := sha256.New()
	h.Write(cut.digest[:])
	h.Write(publication[:])
	chunk := uint32(0)
	payload := make([]byte, 0, searchChangeChunkBytes)
	flush := func() {
		chunk++
		b.Put(binary.BigEndian.AppendUint32(bytes.Clone(prefix), chunk), payload)
		h.Write(payload)
		payload = payload[:0]
	}
	for _, key := range keys {
		if len(payload)+binary.MaxVarintLen64+len(key) > searchChangeChunkBytes {
			flush()
		}
		payload = binary.AppendUvarint(payload, uint64(len(key)))
		payload = append(payload, key...)
	}
	if len(payload) != 0 {
		flush()
	}

	copy(cut.digest[:], h.Sum(nil))
	footer := binary.BigEndian.AppendUint32(nil, chunk)
	footer = binary.BigEndian.AppendUint64(footer, uint64(len(keys)))
	footer = append(footer, publication[:]...)
	b.Put(tarpSearchChangeKey(gen, cut.last, 0), footer)
	b.Put(tarpServiceKey(gen, tarpSearchSource), cut.encode())
	return cut, nil
}

// visitTarpSearchChanges decodes a complete contiguous suffix with explicit
// limits. The visitor may stage work and the caller discards it on any error.
// baseDigest is the source
// digest certified by the selected disk Search base.
func visitTarpSearchChanges(ctx context.Context, snap *tarp.Snapshot, gen uint64, cut tarpSearchCut, baseSeq uint64, baseDigest [sha256.Size]byte, maxBytes, maxKeys int, visit func(seq uint64, key string) error) error {
	if cut.last == math.MaxUint64 || baseSeq > cut.last || baseSeq+1 < cut.first || maxBytes < 0 || maxKeys < 0 || visit == nil {
		return errTarpSearchCut
	}
	for seq := baseSeq + 1; seq <= cut.last; seq++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := snap.GetContext(ctx, tarpSearchChangeKey(gen, seq, 0))
		if err != nil {
			return errors.Join(errTarpSearchCut, err)
		}
		if len(raw) != 12+sha256.Size {
			return errTarpSearchCut
		}
		chunks := binary.BigEndian.Uint32(raw[:4])
		want := binary.BigEndian.Uint64(raw[4:12])
		if chunks == 0 || chunks == math.MaxUint32 || want < uint64(chunks) {
			return errTarpSearchCut
		}
		if want > uint64(maxKeys) {
			return errTarpSearchCut
		}
		h := sha256.New()
		h.Write(baseDigest[:])
		h.Write(raw[12:])
		var count uint64
		prev := ""
		for chunk := uint32(1); chunk <= chunks; chunk++ {
			data, err := snap.GetContext(ctx, tarpSearchChangeKey(gen, seq, chunk))
			if err != nil {
				return errors.Join(errTarpSearchCut, err)
			}
			if len(data) == 0 || len(data) > searchChangeChunkBytes || len(data) > maxBytes {
				return errTarpSearchCut
			}
			maxBytes -= len(data)
			h.Write(data)
			for len(data) != 0 {
				n, used := binary.Uvarint(data)
				if used <= 0 || n == 0 || count == want {
					return errTarpSearchCut
				}
				data = data[used:]
				if n > uint64(len(data)) {
					return errTarpSearchCut
				}
				key := string(data[:n])
				if strings.Compare(prev, key) >= 0 {
					return errTarpSearchCut
				}
				if err := visit(seq, key); err != nil {
					return err
				}
				prev = key
				count++
				data = data[n:]
			}
		}
		if count != want {
			return errTarpSearchCut
		}
		maxKeys -= int(count)
		copy(baseDigest[:], h.Sum(nil))
	}
	if baseDigest != cut.digest {
		return errTarpSearchCut
	}
	return nil
}

// stageTarpGraphSearchChanges observes the final row postimage, excluding
// unchanged endpoint writes. The optional admission hook sees that plan and
// the same pinned Graph preimage, before either effect or journal can commit.
func stageTarpGraphSearchChanges(ctx context.Context, gen uint64, snap *tarp.Snapshot, batch graphstore.Writer, previous, publication [sha256.Size]byte, stage func(graphstore.Writer) error, check func(tarpSearchCut, searchPostimages) error) error {
	rows, err := diskRows()
	if err != nil {
		return err
	}
	const maxSearchProposalBytes = 32 << 20
	proposal := searchPostimages{rows: rows, changed: make(map[string][]byte)}
	var retained int
	tracker := rows.TrackVertexWrites(ctx, snap, batch, func(key string, changed bool, value []byte) error {
		retained -= len(proposal.changed[key])
		if !changed {
			delete(proposal.changed, key)
		} else if check == nil {
			proposal.changed[key] = nil
		} else {
			if len(value) > maxSearchProposalBytes-retained {
				return &mutationlog.DefiniteWALAbort{Cause: &diskSearchAdmissionRejection{cause: &search.BudgetExceededError{Kind: search.WorkDiskReadBytes, Limit: maxSearchProposalBytes}}}
			}
			proposal.changed[key] = bytes.Clone(value)
			retained += len(value)
		}
		return nil
	})
	if err := stage(tracker); err != nil {
		return err
	}
	if err := tracker.Err(); err != nil {
		return err
	}
	if len(proposal.changed) == 0 {
		return nil
	}
	for key := range proposal.changed {
		proposal.keys = append(proposal.keys, key)
	}
	slices.Sort(proposal.keys)
	cut, err := readTarpSearchCut(ctx, snap, gen)
	if errors.Is(err, tarp.ErrNotFound) {
		cut = tarpSearchCut{epoch: gen, first: 1, digest: previous}
	} else if err != nil {
		return err
	}
	if check != nil {
		if err := check(cut, proposal); err != nil {
			return &mutationlog.DefiniteWALAbort{Cause: &diskSearchAdmissionRejection{cause: err}}
		}
	}
	_, err = stageTarpSearchChanges(batch, gen, cut, publication, proposal.keys)
	return err
}
