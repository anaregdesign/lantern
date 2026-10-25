package security

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"sort"
)

// ConfiguredTrust is detached public issuer intent. It is not a credential or
// permission to apply a transition; only the enrolled native configuration
// origin and the normal certified prefix can authorize its application.
type ConfiguredTrust struct {
	issuers []Issuer
	digest  [32]byte
}

// NewConfiguredTrust validates supplied issuer intent without opening state or
// resolving secrets. SecretRef is an opaque public binding name. Plaintext
// secrets do not occur in Issuer and are never part of this digest.
func NewConfiguredTrust(issuers []Issuer) (*ConfiguredTrust, error) {
	if len(issuers) > MaxIssuers {
		return nil, ErrInvalidImage
	}
	canonical := make([]Issuer, len(issuers))
	for n, issuer := range issuers {
		if issuer.Deleted || len(issuer.Algorithms) > 4 || len(issuer.NativeClients) > MaxNativeClients {
			return nil, ErrInvalidImage
		}
		for _, client := range issuer.NativeClients {
			if len(client.Presenters) > MaxNativePresenters {
				return nil, ErrInvalidImage
			}
		}
		canonical[n] = configuredIssuerSemantics(issuer)
		if ValidateIssuerConfiguration(canonical[n]) != nil {
			return nil, ErrInvalidImage
		}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].URL < canonical[j].URL })
	for n := 1; n < len(canonical); n++ {
		if canonical[n-1].URL == canonical[n].URL {
			return nil, ErrInvalidImage
		}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil || len(encoded) > MaxImageBytes {
		return nil, ErrInvalidImage
	}
	return &ConfiguredTrust{issuers: canonical, digest: s1Digest("configured-public-issuers-v1", canonical)}, nil
}

func (c *ConfiguredTrust) Digest() [32]byte {
	if c == nil {
		return [32]byte{}
	}
	return c.digest
}

func (c *ConfiguredTrust) valid() bool {
	return c != nil && c.digest != [32]byte{} && c.digest == s1Digest("configured-public-issuers-v1", c.issuers)
}

func configuredIssuerSemantics(issuer Issuer) Issuer {
	issuer = issuer.clone()
	issuer.ConfigRevision, issuer.EnvOwned = 0, false
	slices.Sort(issuer.Algorithms)
	if len(issuer.NativeClients) == 0 {
		issuer.NativeClients = nil
	}
	sort.Slice(issuer.NativeClients, func(i, j int) bool { return issuer.NativeClients[i].ID < issuer.NativeClients[j].ID })
	for n := range issuer.NativeClients {
		slices.Sort(issuer.NativeClients[n].Presenters)
		if len(issuer.NativeClients[n].Presenters) == 0 {
			issuer.NativeClients[n].Presenters = nil
		}
	}
	return issuer
}

// assessConfiguredTrust is a pure deterministic planner. The revision below is
// retained security history, derived from the exact predecessor, never a user
// supplied revision or a persisted configuration-file fallback.
func assessConfiguredTrust(p *S1Projection, desired *ConfiguredTrust) (*S1Projection, error) {
	if p == nil || p.snapshot == nil || !desired.valid() {
		return nil, ErrS1Contract
	}
	image := p.Image()
	digest := hex.EncodeToString(desired.digest[:])
	if image.ConfiguredTrustDigest == digest {
		return p, nil
	}
	if image.ConfiguredTrustRevision == math.MaxUint64 || p.cut.Sequence == math.MaxUint64 {
		return nil, ErrS1Contract
	}
	next := &S1Projection{cut: p.cut, lineage: make(map[Identity]uint64, len(p.lineage))}
	for identity, epoch := range p.lineage {
		next.lineage[identity] = epoch
	}
	indices := make(map[string]int, len(image.Issuers))
	for n, issuer := range image.Issuers {
		indices[issuer.URL] = n
	}
	changed := make(map[string]bool)
	reenabled := make(map[string]bool)
	retained := make(map[string]bool, len(desired.issuers))
	for _, issuer := range desired.issuers {
		retained[issuer.URL] = true
		index, exists := indices[issuer.URL]
		if !exists {
			issuer.ConfigRevision, issuer.EnvOwned = 1, true
			image.Issuers = append(image.Issuers, issuer.clone())
			continue
		}
		old := image.Issuers[index]
		// Configuration cannot appropriate a management-owned trust object,
		// including a tombstone retained by a management operation.
		if !old.EnvOwned {
			return nil, ErrBootstrapLocked
		}
		if reflect.DeepEqual(configuredIssuerSemantics(old), issuer) {
			continue
		}
		if old.ConfigRevision == math.MaxUint64 {
			return nil, ErrS1Contract
		}
		issuer.ConfigRevision, issuer.EnvOwned = old.ConfigRevision+1, true
		image.Issuers[index] = issuer.clone()
		changed[issuer.URL] = true
		reenabled[issuer.URL] = issuer.Enabled && (!old.Enabled || old.Deleted)
	}
	for n := range image.Issuers {
		issuer := &image.Issuers[n]
		if !issuer.EnvOwned || retained[issuer.URL] || issuer.Deleted {
			continue
		}
		if issuer.ConfigRevision == math.MaxUint64 {
			return nil, ErrS1Contract
		}
		issuer.Enabled, issuer.Deleted = false, true
		issuer.ConfigRevision++
		changed[issuer.URL] = true
	}
	for identity, epoch := range next.lineage {
		if !changed[identity.Issuer] {
			continue
		}
		if epoch == math.MaxUint64 {
			return nil, ErrS1Contract
		}
		next.lineage[identity] = epoch + 1
		revokePrincipalSessions(&image, identity)
	}
	image.ConfiguredTrustRevision++
	image.ConfiguredTrustDigest = digest
	var err error
	next.snapshot, err = compileImage(image, p.snapshot.limits, p.snapshot)
	if err != nil {
		return nil, err
	}
	// Existing assignments remain untouched. Re-enabling a withdrawn issuer
	// must not reactivate any effective permission carried by its old subjects.
	// This path has no enrollment-proof input; such grants require an explicit
	// legitimate enrollment/management operation rather than configuration.
	for identity := range next.snapshot.principals {
		if !reenabled[identity.Issuer] {
			continue
		}
		if !configuredPermissionsSubset(p.snapshot, next.snapshot, identity) {
			return nil, ErrPermissionDenied
		}
	}
	next.cut.Sequence++
	next.cut.Previous = s1Digest("cut", p.cut)
	next.cut.Projection = next.projectionDigest()
	next.cut.Frontier = s1Digest("configured-trust-event-v1", struct {
		Previous [32]byte
		Revision uint64
		Digest   [32]byte
	}{p.cut.Frontier, image.ConfiguredTrustRevision, desired.digest})
	return next, nil
}

func configuredPermissionsSubset(before, after *Snapshot, identity Identity) bool {
	principal, known := after.principals[identity]
	if !known {
		return true
	}
	// Reuse the complete management nonexpansion rule, restricted to this
	// issuer's exact identity, so future permission kinds stay covered here.
	selected := *after
	selected.principals = map[Identity]principalAccess{identity: principal}
	return s1PermissionsSubset(before, &selected)
}
