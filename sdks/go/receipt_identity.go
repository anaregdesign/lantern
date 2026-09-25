package client

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	// ReceiptOperationIDSize is the canonical v1 operation-ID width.
	ReceiptOperationIDSize = 49
	// ReceiptOperationRandomSize is the CSPRNG suffix width in an operation ID.
	ReceiptOperationRandomSize = 24
	// ReceiptGroupIDSize is the logical-call identity width.
	ReceiptGroupIDSize = 16
	// ReceiptEpochSize is the deployment receipt-epoch width.
	ReceiptEpochSize = 16
	// ReceiptNodeIDSize is the endpoint node-identity width.
	ReceiptNodeIDSize = 16
	// ReceiptGenerationSize is the endpoint generation width.
	ReceiptGenerationSize = 16
	// ReceiptPolicyFingerprintSize is the receipt-policy SHA-256 width.
	ReceiptPolicyFingerprintSize = 32
	// ReceiptIntentSHA256Size is the canonical intent-digest width.
	ReceiptIntentSHA256Size = 32

	receiptOperationIDVersion = byte(1)
)

var (
	// ErrInvalidReceipt reports malformed receipt identity, context, or status
	// input. Local validation errors also match ErrInvalidArgument.
	ErrInvalidReceipt = errors.New("invalid mutation receipt")
	// ErrReceiptsDisabled reports an enabled receipt capability was required
	// but the endpoint explicitly reported receipts disabled.
	ErrReceiptsDisabled = errors.New("mutation receipts are disabled")
	// ErrReceiptProtocol reports a malformed or internally inconsistent
	// receipt response from an endpoint.
	ErrReceiptProtocol = errors.New("invalid mutation receipt response")
	// ErrReceiptReconciliationRequired marks an uncertain mutation that may
	// only be reconciled through receipt status; it must not be blindly sent
	// to a different endpoint continuity marker.
	ErrReceiptReconciliationRequired = errors.New("mutation receipt reconciliation required")
)

// ReceiptEpoch is the deployment-wide receipt identity. It is independent of
// authentication credentials and remains stable across bearer-token rotation.
type ReceiptEpoch [ReceiptEpochSize]byte

// ReceiptNodeID identifies the process that accepted a receipt-bearing
// mutation.
type ReceiptNodeID [ReceiptNodeIDSize]byte

// ReceiptGeneration identifies one certified receipt-state generation on a
// node.
type ReceiptGeneration [ReceiptGenerationSize]byte

// ReceiptPolicyFingerprint is the SHA-256 fingerprint of the immutable
// receipt policy for an active epoch.
type ReceiptPolicyFingerprint [ReceiptPolicyFingerprintSize]byte

// ReceiptIntentSHA256 is the server-authored digest of one canonical mutation
// intent.
type ReceiptIntentSHA256 [ReceiptIntentSHA256Size]byte

// ReceiptGroupID identifies one ordered logical call.
type ReceiptGroupID [ReceiptGroupIDSize]byte

// ReceiptOperationID is the canonical v1 operation identity: version, epoch,
// UTC issuance milliseconds, and 24 bytes of cryptographic randomness.
type ReceiptOperationID [ReceiptOperationIDSize]byte

// ReceiptContinuity is the complete marker that authorizes a same-endpoint
// retry: deployment epoch, node identity, and certified generation.
type ReceiptContinuity struct {
	Epoch      ReceiptEpoch
	NodeID     ReceiptNodeID
	Generation ReceiptGeneration
}

// ReceiptCapability is an authenticated endpoint's receipt preflight result.
// Disabled capabilities intentionally carry only Enabled=false.
type ReceiptCapability struct {
	Enabled           bool
	Continuity        ReceiptContinuity
	PolicyFingerprint ReceiptPolicyFingerprint
	Retention         time.Duration
	MaxEntries        uint64
	MaxBytes          uint64
	ServerTime        time.Time
}

// ReceiptContext is the exact caller-owned identity needed to send or replay
// one logical receipt-bearing mutation. Persist it before the first send and
// reuse it byte-for-byte after an ambiguous response.
type ReceiptContext struct {
	Continuity   ReceiptContinuity
	GroupID      ReceiptGroupID
	OperationIDs []ReceiptOperationID
}

// ReceiptReconciliationError reports that the endpoint which would receive an
// uncertain mutation is disabled or no longer has the expected continuity.
// Call GetReceiptStatus/GetReceiptStatuses; do not retry the mutation on a
// different endpoint.
type ReceiptReconciliationError struct {
	Expected ReceiptContinuity
	Observed *ReceiptContinuity
	Cause    error
}

func (e *ReceiptReconciliationError) Error() string {
	if e == nil {
		return ErrReceiptReconciliationRequired.Error()
	}
	if e.Observed == nil {
		return fmt.Sprintf("%s: expected %s: %v", ErrReceiptReconciliationRequired, e.Expected, e.Cause)
	}
	return fmt.Sprintf("%s: expected %s, observed %s: %v", ErrReceiptReconciliationRequired, e.Expected, *e.Observed, e.Cause)
}

func (e *ReceiptReconciliationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ReceiptReconciliationError) Is(target error) bool {
	return target == ErrReceiptReconciliationRequired
}

// NewReceiptOperationID encodes caller-supplied CSPRNG bytes into the one
// canonical operation-ID layout.
func NewReceiptOperationID(epoch ReceiptEpoch, issuedAt time.Time, random [ReceiptOperationRandomSize]byte) (ReceiptOperationID, error) {
	if err := validateReceiptEpoch(epoch); err != nil {
		return ReceiptOperationID{}, err
	}
	if random == ([ReceiptOperationRandomSize]byte{}) || issuedAt.UnixMilli() < 0 {
		return ReceiptOperationID{}, invalidReceiptError("operation ID requires nonzero randomness and a nonnegative issuance time")
	}
	var id ReceiptOperationID
	id[0] = receiptOperationIDVersion
	copy(id[1:17], epoch[:])
	binary.BigEndian.PutUint64(id[17:25], uint64(issuedAt.UnixMilli()))
	copy(id[25:], random[:])
	return id, nil
}

// ReceiptOperationIDFromBytes validates and copies a wire operation ID.
func ReceiptOperationIDFromBytes(raw []byte) (ReceiptOperationID, error) {
	if len(raw) != ReceiptOperationIDSize {
		return ReceiptOperationID{}, invalidReceiptError("operation ID must be %d bytes, got %d", ReceiptOperationIDSize, len(raw))
	}
	var id ReceiptOperationID
	copy(id[:], raw)
	if err := validateReceiptOperationID(id); err != nil {
		return ReceiptOperationID{}, err
	}
	return id, nil
}

// ParseReceiptOperationID decodes a canonical hexadecimal operation ID.
func ParseReceiptOperationID(encoded string) (ReceiptOperationID, error) {
	raw, err := decodeReceiptHex("operation ID", encoded, ReceiptOperationIDSize)
	if err != nil {
		return ReceiptOperationID{}, err
	}
	return ReceiptOperationIDFromBytes(raw)
}

// Bytes returns a copy of the canonical wire representation.
func (id ReceiptOperationID) Bytes() []byte { return append([]byte(nil), id[:]...) }

// String returns the canonical lowercase hexadecimal representation.
func (id ReceiptOperationID) String() string { return hex.EncodeToString(id[:]) }

func (id ReceiptOperationID) MarshalText() ([]byte, error) {
	if err := validateReceiptOperationID(id); err != nil {
		return nil, err
	}
	return []byte(id.String()), nil
}

func (id *ReceiptOperationID) UnmarshalText(text []byte) error {
	parsed, err := ParseReceiptOperationID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Epoch returns the validated deployment epoch encoded in id.
func (id ReceiptOperationID) Epoch() (ReceiptEpoch, error) {
	if err := validateReceiptOperationID(id); err != nil {
		return ReceiptEpoch{}, err
	}
	var epoch ReceiptEpoch
	copy(epoch[:], id[1:17])
	return epoch, nil
}

// IssuedAt returns the validated UTC millisecond issuance time encoded in id.
func (id ReceiptOperationID) IssuedAt() (time.Time, error) {
	if err := validateReceiptOperationID(id); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(int64(binary.BigEndian.Uint64(id[17:25]))).UTC(), nil
}

// ReceiptGroupIDFromBytes validates and copies a logical-call ID.
func ReceiptGroupIDFromBytes(raw []byte) (ReceiptGroupID, error) {
	var id ReceiptGroupID
	if err := copyNonzeroReceiptBytes("logical-call ID", id[:], raw); err != nil {
		return ReceiptGroupID{}, err
	}
	return id, nil
}

// ParseReceiptGroupID decodes a canonical hexadecimal logical-call ID.
func ParseReceiptGroupID(encoded string) (ReceiptGroupID, error) {
	raw, err := decodeReceiptHex("logical-call ID", encoded, ReceiptGroupIDSize)
	if err != nil {
		return ReceiptGroupID{}, err
	}
	return ReceiptGroupIDFromBytes(raw)
}

func (id ReceiptGroupID) Bytes() []byte  { return append([]byte(nil), id[:]...) }
func (id ReceiptGroupID) String() string { return hex.EncodeToString(id[:]) }
func (id ReceiptGroupID) MarshalText() ([]byte, error) {
	if id == (ReceiptGroupID{}) {
		return nil, invalidReceiptError("logical-call ID must be nonzero")
	}
	return []byte(id.String()), nil
}
func (id *ReceiptGroupID) UnmarshalText(text []byte) error {
	parsed, err := ParseReceiptGroupID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ReceiptEpochFromBytes validates and copies a deployment epoch.
func ReceiptEpochFromBytes(raw []byte) (ReceiptEpoch, error) {
	var id ReceiptEpoch
	if err := copyNonzeroReceiptBytes("deployment epoch", id[:], raw); err != nil {
		return ReceiptEpoch{}, err
	}
	return id, nil
}

// ParseReceiptEpoch decodes a canonical hexadecimal deployment epoch.
func ParseReceiptEpoch(encoded string) (ReceiptEpoch, error) {
	raw, err := decodeReceiptHex("deployment epoch", encoded, ReceiptEpochSize)
	if err != nil {
		return ReceiptEpoch{}, err
	}
	return ReceiptEpochFromBytes(raw)
}

func (id ReceiptEpoch) Bytes() []byte  { return append([]byte(nil), id[:]...) }
func (id ReceiptEpoch) String() string { return hex.EncodeToString(id[:]) }
func (id ReceiptEpoch) MarshalText() ([]byte, error) {
	if err := validateReceiptEpoch(id); err != nil {
		return nil, err
	}
	return []byte(id.String()), nil
}
func (id *ReceiptEpoch) UnmarshalText(text []byte) error {
	parsed, err := ParseReceiptEpoch(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ReceiptNodeIDFromBytes validates and copies an endpoint node ID.
func ReceiptNodeIDFromBytes(raw []byte) (ReceiptNodeID, error) {
	var id ReceiptNodeID
	if err := copyNonzeroReceiptBytes("receipt node ID", id[:], raw); err != nil {
		return ReceiptNodeID{}, err
	}
	return id, nil
}

// ParseReceiptNodeID decodes a canonical hexadecimal endpoint node ID.
func ParseReceiptNodeID(encoded string) (ReceiptNodeID, error) {
	raw, err := decodeReceiptHex("receipt node ID", encoded, ReceiptNodeIDSize)
	if err != nil {
		return ReceiptNodeID{}, err
	}
	return ReceiptNodeIDFromBytes(raw)
}

func (id ReceiptNodeID) Bytes() []byte  { return append([]byte(nil), id[:]...) }
func (id ReceiptNodeID) String() string { return hex.EncodeToString(id[:]) }
func (id ReceiptNodeID) MarshalText() ([]byte, error) {
	if id == (ReceiptNodeID{}) {
		return nil, invalidReceiptError("receipt node ID must be nonzero")
	}
	return []byte(id.String()), nil
}
func (id *ReceiptNodeID) UnmarshalText(text []byte) error {
	parsed, err := ParseReceiptNodeID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ReceiptGenerationFromBytes validates and copies an endpoint generation.
func ReceiptGenerationFromBytes(raw []byte) (ReceiptGeneration, error) {
	var id ReceiptGeneration
	if err := copyNonzeroReceiptBytes("receipt generation", id[:], raw); err != nil {
		return ReceiptGeneration{}, err
	}
	return id, nil
}

// ParseReceiptGeneration decodes a canonical hexadecimal endpoint generation.
func ParseReceiptGeneration(encoded string) (ReceiptGeneration, error) {
	raw, err := decodeReceiptHex("receipt generation", encoded, ReceiptGenerationSize)
	if err != nil {
		return ReceiptGeneration{}, err
	}
	return ReceiptGenerationFromBytes(raw)
}

func (id ReceiptGeneration) Bytes() []byte  { return append([]byte(nil), id[:]...) }
func (id ReceiptGeneration) String() string { return hex.EncodeToString(id[:]) }
func (id ReceiptGeneration) MarshalText() ([]byte, error) {
	if id == (ReceiptGeneration{}) {
		return nil, invalidReceiptError("receipt generation must be nonzero")
	}
	return []byte(id.String()), nil
}
func (id *ReceiptGeneration) UnmarshalText(text []byte) error {
	parsed, err := ParseReceiptGeneration(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ReceiptPolicyFingerprintFromBytes validates and copies a policy fingerprint.
func ReceiptPolicyFingerprintFromBytes(raw []byte) (ReceiptPolicyFingerprint, error) {
	var id ReceiptPolicyFingerprint
	if err := copyNonzeroReceiptBytes("receipt policy fingerprint", id[:], raw); err != nil {
		return ReceiptPolicyFingerprint{}, err
	}
	return id, nil
}

// ParseReceiptPolicyFingerprint decodes a canonical hexadecimal policy
// fingerprint.
func ParseReceiptPolicyFingerprint(encoded string) (ReceiptPolicyFingerprint, error) {
	raw, err := decodeReceiptHex("receipt policy fingerprint", encoded, ReceiptPolicyFingerprintSize)
	if err != nil {
		return ReceiptPolicyFingerprint{}, err
	}
	return ReceiptPolicyFingerprintFromBytes(raw)
}

func (id ReceiptPolicyFingerprint) Bytes() []byte  { return append([]byte(nil), id[:]...) }
func (id ReceiptPolicyFingerprint) String() string { return hex.EncodeToString(id[:]) }
func (id ReceiptPolicyFingerprint) MarshalText() ([]byte, error) {
	if id == (ReceiptPolicyFingerprint{}) {
		return nil, invalidReceiptError("receipt policy fingerprint must be nonzero")
	}
	return []byte(id.String()), nil
}
func (id *ReceiptPolicyFingerprint) UnmarshalText(text []byte) error {
	parsed, err := ParseReceiptPolicyFingerprint(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id ReceiptIntentSHA256) Bytes() []byte  { return append([]byte(nil), id[:]...) }
func (id ReceiptIntentSHA256) String() string { return hex.EncodeToString(id[:]) }
func (id ReceiptIntentSHA256) MarshalText() ([]byte, error) {
	if id == (ReceiptIntentSHA256{}) {
		return nil, invalidReceiptError("intent SHA-256 must be nonzero")
	}
	return []byte(id.String()), nil
}

func (c ReceiptContinuity) String() string {
	return fmt.Sprintf("epoch=%s node=%s generation=%s", c.Epoch, c.NodeID, c.Generation)
}

// Validate rejects zero or malformed endpoint continuity.
func (c ReceiptContinuity) Validate() error {
	if err := validateReceiptEpoch(c.Epoch); err != nil {
		return err
	}
	if c.NodeID == (ReceiptNodeID{}) {
		return invalidReceiptError("receipt node ID must be nonzero")
	}
	if c.Generation == (ReceiptGeneration{}) {
		return invalidReceiptError("receipt generation must be nonzero")
	}
	return nil
}

// Validate checks the complete capability shape, including the rule that a
// disabled capability carries no identity-bearing fields.
func (c ReceiptCapability) Validate() error {
	if !c.Enabled {
		if c.Continuity != (ReceiptContinuity{}) ||
			c.PolicyFingerprint != (ReceiptPolicyFingerprint{}) ||
			c.Retention != 0 || c.MaxEntries != 0 || c.MaxBytes != 0 ||
			!c.ServerTime.IsZero() {
			return invalidReceiptError("disabled capability carried receipt identity or policy")
		}
		return nil
	}
	if err := c.Continuity.Validate(); err != nil {
		return err
	}
	if c.PolicyFingerprint == (ReceiptPolicyFingerprint{}) {
		return invalidReceiptError("receipt policy fingerprint must be nonzero")
	}
	if c.Retention <= 0 || c.MaxEntries == 0 || c.MaxBytes == 0 || c.ServerTime.IsZero() || c.ServerTime.UnixMilli() < 0 {
		return invalidReceiptError("enabled capability has invalid policy limits or server time")
	}
	return nil
}

// Validate checks a persisted context against the intended item count.
func (c ReceiptContext) Validate(itemCount int) error {
	if itemCount <= 0 || uint64(itemCount) > math.MaxUint32 {
		return invalidReceiptError("receipt item count must be between 1 and %d", uint64(math.MaxUint32))
	}
	if err := c.Continuity.Validate(); err != nil {
		return err
	}
	if c.GroupID == (ReceiptGroupID{}) {
		return invalidReceiptError("logical-call ID must be nonzero")
	}
	if len(c.OperationIDs) != itemCount {
		return invalidReceiptError("operation ID count %d does not match item count %d", len(c.OperationIDs), itemCount)
	}
	seen := make(map[ReceiptOperationID]struct{}, len(c.OperationIDs))
	for i, id := range c.OperationIDs {
		if err := validateReceiptOperationID(id); err != nil {
			return invalidReceiptError("operation_ids[%d]: %v", i, err)
		}
		epoch, _ := id.Epoch()
		if epoch != c.Continuity.Epoch {
			return invalidReceiptError("operation_ids[%d] belongs to a different deployment epoch", i)
		}
		if _, duplicate := seen[id]; duplicate {
			return invalidReceiptError("operation_ids[%d] duplicates an earlier item", i)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// Clone returns a detached copy suitable for retaining across request
// lifetimes.
func (c ReceiptContext) Clone() ReceiptContext {
	c.OperationIDs = append([]ReceiptOperationID(nil), c.OperationIDs...)
	return c
}

type receiptIdentitySource struct {
	random io.Reader
	now    func() time.Time
}

func defaultReceiptIdentitySource() receiptIdentitySource {
	return receiptIdentitySource{random: cryptorand.Reader, now: time.Now}
}

func (s receiptIdentitySource) normalized() receiptIdentitySource {
	if s.random == nil {
		s.random = cryptorand.Reader
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// NewReceiptContext mints one caller-owned logical-call context from a fresh
// enabled capability. The context is not sent or retained by the client.
func (l *Lantern) NewReceiptContext(capability ReceiptCapability, itemCount int) (ReceiptContext, error) {
	source := defaultReceiptIdentitySource()
	if l != nil {
		source = l.receiptIDs.normalized()
	}
	return mintReceiptContext(capability, itemCount, source)
}

func mintReceiptContext(capability ReceiptCapability, itemCount int, source receiptIdentitySource) (ReceiptContext, error) {
	if err := capability.Validate(); err != nil {
		return ReceiptContext{}, err
	}
	if !capability.Enabled {
		return ReceiptContext{}, errors.Join(ErrFailedPrecondition, ErrReceiptsDisabled)
	}
	if itemCount <= 0 || uint64(itemCount) > math.MaxUint32 {
		return ReceiptContext{}, invalidReceiptError("receipt item count must be between 1 and %d", uint64(math.MaxUint32))
	}
	source = source.normalized()
	issuedAt := source.now().UTC()
	if issuedAt.UnixMilli() < 0 {
		return ReceiptContext{}, invalidReceiptError("receipt issuance time must be nonnegative")
	}
	var group ReceiptGroupID
	if _, err := io.ReadFull(source.random, group[:]); err != nil {
		return ReceiptContext{}, fmt.Errorf("client: mint receipt logical-call ID: %w", err)
	}
	if group == (ReceiptGroupID{}) {
		return ReceiptContext{}, invalidReceiptError("minted logical-call ID must be nonzero")
	}
	ids := make([]ReceiptOperationID, itemCount)
	for i := range ids {
		var random [ReceiptOperationRandomSize]byte
		if _, err := io.ReadFull(source.random, random[:]); err != nil {
			return ReceiptContext{}, fmt.Errorf("client: mint receipt operation_ids[%d]: %w", i, err)
		}
		id, err := NewReceiptOperationID(capability.Continuity.Epoch, issuedAt, random)
		if err != nil {
			return ReceiptContext{}, fmt.Errorf("client: mint receipt operation_ids[%d]: %w", i, err)
		}
		ids[i] = id
	}
	return ReceiptContext{
		Continuity:   capability.Continuity,
		GroupID:      group,
		OperationIDs: ids,
	}, nil
}

func validateReceiptOperationID(id ReceiptOperationID) error {
	if id[0] != receiptOperationIDVersion {
		return invalidReceiptError("operation ID has unknown version %d", id[0])
	}
	var epoch ReceiptEpoch
	copy(epoch[:], id[1:17])
	if err := validateReceiptEpoch(epoch); err != nil {
		return err
	}
	if binary.BigEndian.Uint64(id[17:25]) > math.MaxInt64 {
		return invalidReceiptError("operation ID issuance time is out of range")
	}
	if allZero(id[25:]) {
		return invalidReceiptError("operation ID randomness must be nonzero")
	}
	return nil
}

func validateReceiptEpoch(epoch ReceiptEpoch) error {
	if epoch == (ReceiptEpoch{}) {
		return invalidReceiptError("deployment epoch must be nonzero")
	}
	return nil
}

func copyNonzeroReceiptBytes(name string, dst, raw []byte) error {
	if len(raw) != len(dst) {
		return invalidReceiptError("%s must be %d bytes, got %d", name, len(dst), len(raw))
	}
	if allZero(raw) {
		return invalidReceiptError("%s must be nonzero", name)
	}
	copy(dst, raw)
	return nil
}

func decodeReceiptHex(name, encoded string, size int) ([]byte, error) {
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, invalidReceiptError("%s hex: %v", name, err)
	}
	if len(raw) != size {
		return nil, invalidReceiptError("%s must decode to %d bytes, got %d", name, size, len(raw))
	}
	return raw, nil
}

func allZero(value []byte) bool {
	return len(value) == 0 || bytes.Equal(value, make([]byte, len(value)))
}

func invalidReceiptError(format string, args ...any) error {
	return errors.Join(ErrInvalidArgument, ErrInvalidReceipt, fmt.Errorf(format, args...))
}

func receiptProtocolError(format string, args ...any) error {
	return errors.Join(ErrReceiptProtocol, fmt.Errorf(format, args...))
}
