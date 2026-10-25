package security

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/server/internal/authz"
)

const (
	ImageVersion       = 2
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
	Version                 int                 `json:"version"`
	BootstrapRevision       uint64              `json:"bootstrap_revision"`
	BootstrapDigest         string              `json:"bootstrap_digest,omitempty"`
	ConfiguredTrustRevision uint64              `json:"configured_trust_revision,omitempty"`
	ConfiguredTrustDigest   string              `json:"configured_trust_digest,omitempty"`
	Issuers                 []Issuer            `json:"issuers"`
	Roles                   []Role              `json:"roles"`
	Principals              []Principal         `json:"principals"`
	Sessions                []Session           `json:"sessions"`
	MachineCredentials      []MachineCredential `json:"machine_credentials,omitempty"`
	Audit                   []AuditRecord       `json:"audit,omitempty"`
	RegistrationPolicy      *RegistrationPolicy `json:"registration_policy,omitempty"`
}

// CompileImage validates the complete image before any durable commit. A
// snapshot owns all compiled input and caps total materialized metadata bytes.
func CompileImage(image Image, limits PolicyLimits) (*Snapshot, error) {
	return compileImage(image, limits, nil)
}

func compileImage(image Image, limits PolicyLimits, previous *Snapshot) (*Snapshot, error) {
	return compileImageAdmission(image, limits, previous, true)
}

// Historical images retain their original signed encoding and validation
// contract. New writes always require a qualified human administrator. Reading
// a legacy cut does not enroll its OIDC principals or permit their mutations.
func compileImageAdmission(image Image, limits PolicyLimits, previous *Snapshot, requireHuman bool) (*Snapshot, error) {
	if (image.ConfiguredTrustRevision == 0) != (image.ConfiguredTrustDigest == "") || image.ConfiguredTrustDigest != "" && !validHexDigest(image.ConfiguredTrustDigest) || image.Version != ImageVersion || image.BootstrapDigest != "" && !validHexDigest(image.BootstrapDigest) || len(image.Issuers) > MaxIssuers ||
		len(image.Principals) > MaxPrincipals || len(image.Sessions) > MaxSessions || len(image.Audit) > retainedChanges || len(image.MachineCredentials) > MaxMachineCredentials {
		return nil, ErrInvalidImage
	}
	if err := validateAudit(image.Audit); err != nil {
		return nil, err
	}
	if !image.RegistrationPolicy.valid() {
		return nil, ErrInvalidImage
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
	snapshot := &Snapshot{configuredTrustDigest: image.ConfiguredTrustDigest, policy: policy, issuers: make(map[string]Issuer, len(image.Issuers)),
		principals: make(map[Identity]principalAccess, len(image.Principals)), sessions: make(map[string]Session, len(image.Sessions))}
	snapshot.accessSets = make(map[string]*Access)
	snapshot.roleBytes = roleBytes
	snapshot.limits = limits
	for _, issuer := range image.Issuers {
		if err := ValidateIssuerConfiguration(issuer); err != nil {
			return nil, fmt.Errorf("%w: Issuer", err)
		}
		if _, exists := snapshot.issuers[issuer.URL]; exists {
			return nil, fmt.Errorf("%w: duplicate Issuer", ErrInvalidImage)
		}
		issuer = issuer.clone()
		snapshot.issuers[issuer.URL] = issuer
	}
	if err := validateRegistrationPolicy(image.RegistrationPolicy, snapshot); err != nil {
		return nil, err
	}
	uids := make(map[string]Identity)
	for _, principal := range image.Principals {
		if principal.UserUID != "" {
			if principal.Identity.Kind != OIDCPrincipal || !authz.ValidPersonalUID(principal.UserUID) {
				return nil, fmt.Errorf("%w: Principal UID", ErrInvalidImage)
			}
			if _, exists := uids[principal.UserUID]; exists {
				return nil, fmt.Errorf("%w: duplicate Principal UID", ErrInvalidImage)
			}
			uids[principal.UserUID] = principal.Identity
		}
		if principal.HumanIssuerConfigRevision != 0 && principal.Identity.Kind != OIDCPrincipal {
			return nil, ErrInvalidImage
		}
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
		// Only the current and previous bounded Principal membership sets
		// retain views. Both role membership and trusted UID bind reuse; users
		// with the same Role must never share each other's personal ranges.
		bindings := authz.SubjectBindings{PersonalUID: principal.UserUID}
		setKey, err := policy.AccessKey(ids, bindings)
		if err != nil {
			return nil, err
		}
		access := snapshot.accessSets[setKey]
		if access == nil && previous != nil && policy == previous.policy {
			access = previous.accessSets[setKey]
		}
		if access == nil {
			access, err = policy.BindRoles(ids, bindings)
			if err != nil {
				return nil, err
			}
		}
		snapshot.accessSets[setKey] = access
		bootstrapHuman := false
		for _, assignment := range principal.Assignments {
			bootstrapHuman = bootstrapHuman || image.BootstrapRevision != 0 && assignment.EnvOwned && assignment.RoleID == "security_admin"
		}
		snapshot.principals[principal.Identity] = principalAccess{state: principal.State, access: access, humanIssuerConfigRevision: principal.HumanIssuerConfigRevision, bootstrapHuman: bootstrapHuman, userUID: principal.UserUID}
	}
	if err := snapshot.compileMachines(image.MachineCredentials); err != nil {
		return nil, err
	}
	for _, session := range image.Sessions {
		if !session.valid() {
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
		if requireHuman && !snapshot.HumanIdentity(identity) {
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
	legacy := true
	for _, principal := range image.Principals {
		legacy = legacy && principal.HumanIssuerConfigRevision == 0
	}
	for _, issuer := range image.Issuers {
		legacy = legacy && !issuer.HumanSubjectNamespaceQualified
	}
	snapshot, err := compileImageAdmission(image, limits, nil, !legacy)
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
