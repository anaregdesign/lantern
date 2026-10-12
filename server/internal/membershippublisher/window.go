package membershippublisher

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// validWindow also rejects a cleanup allowance that time.Duration cannot
// represent exactly. Maintenance-scale windows have no short campaign limit.
func validWindow(c Config) bool {
	d := c.CleanupBy.Sub(c.End)
	return !c.End.IsZero() && d > 0 && c.End.Add(d).Equal(c.CleanupBy)
}

// AdvanceWindow authorizes only a later maintenance horizon for the same fixed
// configuration. Both immutable config files must be retained. It changes only
// the journal Binding, signs nothing, and never writes a publication target.
// After success explicitly Open the new config in resume mode. After ambiguous
// persistence retain both files: new journal -> new resume; old expired journal
// -> explicitly repeat this operation, never ordinary expired resume or init.
func AdvanceWindow(configPath, nextConfigPath, statePath string) error {
	return advanceWindowAt(configPath, nextConfigPath, statePath, time.Now().UTC(), systemFiles())
}

func advanceWindowAt(configPath, nextConfigPath, statePath string, now time.Time, files fileOps) (err error) {
	p, err := openOwned(configPath, statePath, "advance-window", now, []string{nextConfigPath})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	p.files = files
	return p.advance(nextConfigPath, now)
}

func (p *Publisher) advance(nextConfigPath string, now time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed || p.closed || !p.retainedOnly {
		return ErrState
	}
	// Even a failed transition requires a new explicit owner and revalidation.
	defer func() { p.failed = true }()
	raw, err := readPrivate(nextConfigPath, maxStateBytes)
	var next Config
	if err != nil || decode(raw, &next) != nil || !validWindow(next) ||
		!next.End.After(p.config.End) || !next.CleanupBy.After(p.config.CleanupBy) ||
		next.CleanupBy.Sub(next.End) < p.config.CleanupBy.Sub(p.config.End) ||
		!now.Add(2*peerauth.ClockMargin).Before(next.End) {
		return ErrState
	}
	unchanged := next
	unchanged.End, unchanged.CleanupBy = p.config.End, p.config.CleanupBy
	if !reflect.DeepEqual(unchanged, p.config) {
		return ErrState
	}
	// The old candidate was already authenticated against the OLD End. Only
	// new issuance capacity uses the current leaf and the NEW cleanup date.
	if _, err = p.validateCapacity(now, next.CleanupBy); err != nil {
		return err
	}
	retained, err := readPrivate(p.path, maxStateBytes)
	if err != nil || !bytes.Equal(retained, p.raw) || p.checkTargets() != nil {
		return ErrState
	}
	if p.files.retained(p.path) != nil {
		return ErrState
	}
	// Recheck after flushing, immediately before the only persistent change.
	retained, err = readPrivate(p.path, maxStateBytes)
	if err != nil || !bytes.Equal(retained, p.raw) {
		return ErrState
	}
	canonical, err := json.Marshal(next)
	if err != nil {
		return ErrState
	}
	p.state.Binding = sha256.Sum256(canonical)
	return p.save()
}
