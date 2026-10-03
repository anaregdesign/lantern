package security

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ImageVersion       = 1
	MaxImageBytes      = 4 << 20
	MaxIssuers         = 64
	MaxPrincipals      = 10000
	MaxSessions        = 10000
	MaxSessionLifetime = 8 * time.Hour
)

var ErrInvalidImage = errors.New("invalid security image")
var ErrLastAdministrator = errors.New("security image requires a usable OIDC administrator")

// Image is one complete typed sys security revision, not independently merged
// graph records. No Principal or Session can carry direct permission rules.
type Image struct {
	Version            int                 `json:"version"`
	BootstrapRevision  uint64              `json:"bootstrap_revision"`
	BootstrapDigest    string              `json:"bootstrap_digest,omitempty"`
	Issuers            []Issuer            `json:"issuers"`
	Roles              []Role              `json:"roles"`
	Principals         []Principal         `json:"principals"`
	Sessions           []Session           `json:"sessions"`
	MachineCredentials []MachineCredential `json:"machine_credentials,omitempty"`
	Audit              []AuditRecord       `json:"audit,omitempty"`
}

// CompileImage validates the complete image before any durable commit. A
// snapshot owns all compiled input and caps total materialized metadata bytes.
func CompileImage(image Image, limits PolicyLimits) (*Snapshot, error) {
	return compileImage(image, limits, nil)
}

func compileImage(image Image, limits PolicyLimits, previous *Snapshot) (*Snapshot, error) {
	if image.Version != ImageVersion || image.BootstrapDigest != "" && !validHexDigest(image.BootstrapDigest) || len(image.Issuers) > MaxIssuers ||
		len(image.Principals) > MaxPrincipals || len(image.Sessions) > MaxSessions || len(image.Audit) > retainedChanges || len(image.MachineCredentials) > MaxMachineCredentials {
		return nil, ErrInvalidImage
	}
	if err := validateAudit(image.Audit); err != nil {
		return nil, err
	}
	// Validate coarse lengths before marshaling untrusted input.
	if err := validateRoleBounds(image.Roles, limits); err != nil {
		return nil, err
	}
	// Sessions and Principal state can change without rebuilding Role ranges.
	// This cache is private and only reuses an already validated policy with
	// identical limits and canonical Role bytes.
	roleBytes, err := json.Marshal(image.Roles)
	if err != nil {
		return nil, ErrInvalidPolicy
	}
	var policy *CompiledPolicy
	if previous != nil && previous.limits == limits && bytes.Equal(roleBytes, previous.roleBytes) {
		policy = previous.policy
	} else {
		policy, err = CompileRoles(image.Roles, limits)
		if err != nil {
			return nil, err
		}
	}
	snapshot := &Snapshot{policy: policy, issuers: make(map[string]Issuer, len(image.Issuers)),
		principals: make(map[Identity]principalAccess, len(image.Principals)), sessions: make(map[string]Session, len(image.Sessions))}
	snapshot.accessSets = make(map[string]*Access)
	snapshot.roleBytes = roleBytes
	snapshot.limits = limits
	for _, issuer := range image.Issuers {
		if issuer.Deleted && issuer.Enabled {
			return nil, ErrInvalidImage
		}
		if !validIssuerURL(issuer.URL) || !boundedText(issuer.ClientID, 512) ||
			!boundedText(issuer.APIAudience, 512) || len(issuer.Algorithms) == 0 || len(issuer.Algorithms) > 4 ||
			len(issuer.SecretRef) > 64 || (issuer.SecretRef != "" && !validRoleID(issuer.SecretRef)) {
			return nil, fmt.Errorf("%w: Issuer", ErrInvalidImage)
		}
		if _, valid := validHTTPSURL(issuer.RedirectURI); !valid {
			return nil, fmt.Errorf("%w: redirect URI", ErrInvalidImage)
		}
		seen := make(map[string]bool, len(issuer.Algorithms))
		for _, algorithm := range issuer.Algorithms {
			if seen[algorithm] {
				return nil, fmt.Errorf("%w: duplicate algorithm", ErrInvalidImage)
			}
			switch algorithm {
			case "RS256", "PS256", "ES256", "EdDSA":
			default:
				return nil, fmt.Errorf("%w: algorithm", ErrInvalidImage)
			}
			seen[algorithm] = true
		}
		if _, exists := snapshot.issuers[issuer.URL]; exists {
			return nil, fmt.Errorf("%w: duplicate Issuer", ErrInvalidImage)
		}
		issuer.Algorithms = append([]string(nil), issuer.Algorithms...)
		snapshot.issuers[issuer.URL] = issuer
	}
	for _, principal := range image.Principals {
		if !principal.Identity.valid() || (principal.State != Active && principal.State != Suspended && principal.State != Deleted) ||
			len(principal.Assignments) > limits.MaxAssignments {
			return nil, fmt.Errorf("%w: Principal", ErrInvalidImage)
		}
		if principal.State == Deleted && len(principal.Assignments) != 0 {
			return nil, ErrInvalidImage
		}
		if principal.Identity.Kind == OIDCPrincipal {
			if _, registered := snapshot.issuers[principal.Identity.Issuer]; !registered {
				return nil, fmt.Errorf("%w: unregistered Issuer", ErrInvalidImage)
			}
		}
		if _, exists := snapshot.principals[principal.Identity]; exists {
			return nil, fmt.Errorf("%w: duplicate Principal", ErrInvalidImage)
		}
		ids := make([]string, len(principal.Assignments))
		for i, assignment := range principal.Assignments {
			ids[i] = assignment.RoleID
		}
		sort.Strings(ids)
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return nil, ErrInvalidPolicy
			}
		}
		// Only the current and previous bounded Principal membership sets
		// retain views; membership churn cannot grow a policy-global cache.
		setKey := strings.Join(ids, "\x00")
		access := snapshot.accessSets[setKey]
		if access == nil && previous != nil && policy == previous.policy {
			access = previous.accessSets[setKey]
		}
		if access == nil {
			access, err = policy.ForRoles(ids)
			if err != nil {
				return nil, err
			}
		}
		snapshot.accessSets[setKey] = access
		snapshot.principals[principal.Identity] = principalAccess{state: principal.State, access: access}
	}
	if err := snapshot.compileMachines(image.MachineCredentials); err != nil {
		return nil, err
	}
	for _, session := range image.Sessions {
		if !validHexDigest(session.Digest) || (session.CSRFDigest != "" && !validHexDigest(session.CSRFDigest)) || session.Identity.Kind != OIDCPrincipal ||
			session.CreatedAt.IsZero() || session.AuthTime.IsZero() || !session.ExpiresAt.After(session.CreatedAt) ||
			session.ExpiresAt.Sub(session.CreatedAt) > MaxSessionLifetime || session.AuthTime.After(session.CreatedAt) {
			return nil, fmt.Errorf("%w: Session", ErrInvalidImage)
		}
		if _, exists := snapshot.principals[session.Identity]; !exists {
			return nil, fmt.Errorf("%w: orphan Session", ErrInvalidImage)
		}
		if _, exists := snapshot.sessions[session.Digest]; exists {
			return nil, fmt.Errorf("%w: duplicate Session", ErrInvalidImage)
		}
		snapshot.sessions[session.Digest] = session
	}
	usableAdmin := false
	for identity := range snapshot.principals {
		if identity.Kind != OIDCPrincipal {
			continue
		}
		access, active := snapshot.AccessFor(identity)
		usableAdmin = usableAdmin || active && access.AllowsGlobal(SecurityManage)
	}
	if !usableAdmin {
		return nil, ErrLastAdministrator
	}
	encoded, err := json.Marshal(image)
	if err != nil || len(encoded) > MaxImageBytes {
		return nil, ErrInvalidImage
	}
	snapshot.image = encoded
	return snapshot, nil
}

func DecodeImage(encoded []byte, limits PolicyLimits) (*Snapshot, error) {
	if len(encoded) == 0 || len(encoded) > MaxImageBytes {
		return nil, ErrInvalidImage
	}
	var image Image
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&image); err != nil {
		return nil, fmt.Errorf("%w: decode", ErrInvalidImage)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing input", ErrInvalidImage)
	}
	snapshot, err := CompileImage(image, limits)
	if err != nil {
		return nil, err
	}
	// Internal persisted images use one canonical encoding. This also rejects
	// duplicate JSON fields, ignored whitespace and alternate number spellings.
	if !bytes.Equal(encoded, snapshot.image) {
		return nil, fmt.Errorf("%w: noncanonical encoding", ErrInvalidImage)
	}
	return snapshot, nil
}

func boundedText(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value)
}

func validHexDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && hex.EncodeToString(decoded) == digest
}
