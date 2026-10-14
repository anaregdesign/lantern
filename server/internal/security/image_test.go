package security

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authz"
)

func TestImageIntegrity(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Image)
		want   error
	}{
		{"version", func(i *Image) { i.Version++ }, ErrInvalidImage},
		{"duplicate Issuer", func(i *Image) { i.Issuers = append(i.Issuers, i.Issuers[0]) }, ErrInvalidImage},
		{"HTTP Issuer", func(i *Image) { i.Issuers[0].URL = "http://idp.example" }, ErrInvalidImage},
		{"unregistered identity", func(i *Image) { i.Principals[0].Identity.Issuer += "/" }, ErrInvalidImage},
		{"unsafe algorithm", func(i *Image) { i.Issuers[0].Algorithms = []string{"HS256"} }, ErrInvalidImage},
		{"duplicate algorithm", func(i *Image) { i.Issuers[0].Algorithms = []string{"RS256", "RS256"} }, ErrInvalidImage},
		{"HTTP redirect", func(i *Image) { i.Issuers[0].RedirectURI = "http://admin.example/callback" }, ErrInvalidImage},
		{"secret URL", func(i *Image) { i.Issuers[0].SecretRef = "https://attacker.example" }, ErrInvalidImage},
		{"duplicate Principal", func(i *Image) { i.Principals = append(i.Principals, i.Principals[0]) }, ErrInvalidImage},
		{"unknown state", func(i *Image) { i.Principals[0].State = "disabled" }, ErrInvalidImage},
		{"unknown Role", func(i *Image) { i.Principals[0].Assignments[0].RoleID = "missing" }, ErrUnknownRole},
		{"duplicate assignment", func(i *Image) {
			i.Principals[0].Assignments = append(i.Principals[0].Assignments, i.Principals[0].Assignments[0])
		}, ErrInvalidPolicy},
		{"last admin suspended", func(i *Image) { i.Principals[0].State = Suspended }, ErrLastAdministrator},
		{"last admin Issuer disabled", func(i *Image) { i.Issuers[0].Enabled = false }, ErrLastAdministrator},
		{"last admin removed", func(i *Image) { i.Principals[0].Assignments = nil }, ErrLastAdministrator},
		{"last admin Role changed", func(i *Image) { i.Roles[0].Rules = nil }, ErrLastAdministrator},
		{"last admin Deny", func(i *Image) { i.Roles[0].Rules = append(i.Roles[0].Rules, globalRule(Deny, SecurityManage)) }, ErrLastAdministrator},
		{"orphan Session", func(i *Image) { i.Sessions = []Session{testSession()}; i.Sessions[0].Identity.Subject = "missing" }, ErrInvalidImage},
		{"long Session", func(i *Image) {
			i.Sessions = []Session{testSession()}
			i.Sessions[0].ExpiresAt = i.Sessions[0].CreatedAt.Add(MaxSessionLifetime + time.Second)
		}, ErrInvalidImage},
		{"future authentication", func(i *Image) {
			i.Sessions = []Session{testSession()}
			i.Sessions[0].AuthTime = i.Sessions[0].CreatedAt.Add(time.Second)
		}, ErrInvalidImage},
		{"invalid digest", func(i *Image) { i.Sessions = []Session{testSession()}; i.Sessions[0].Digest = strings.Repeat("A", 64) }, ErrInvalidImage},
		{"duplicate Session", func(i *Image) { i.Sessions = []Session{testSession(), testSession()} }, ErrInvalidImage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image := testImage()
			test.change(&image)
			if _, err := CompileImage(image, DefaultPolicyLimits()); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestImageCanonicalRecovery(t *testing.T) {
	image := testImage()
	encoded, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeImage(encoded, DefaultPolicyLimits()); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range [][]byte{
		nil, []byte("{"), append(append([]byte(nil), encoded...), []byte("{}")...),
		[]byte(strings.Replace(string(encoded), "\"version\":"+strconv.Itoa(ImageVersion), "\"version\":1,\"version\":"+strconv.Itoa(ImageVersion), 1)),
		[]byte(strings.Replace(string(encoded), "\"version\":"+strconv.Itoa(ImageVersion), "\"unknown\":1,\"version\":"+strconv.Itoa(ImageVersion), 1)),
		append([]byte(" "), encoded...), []byte(strings.Repeat(" ", MaxImageBytes+1)),
	} {
		if _, err := DecodeImage(malformed, DefaultPolicyLimits()); !errors.Is(err, ErrInvalidImage) {
			t.Fatalf("malformed image admitted: %v", err)
		}
	}
}

func TestImageUnknownAuthenticationTimeAndOldReaderBoundary(t *testing.T) {
	image := testImage()
	image.Sessions = []Session{testSession()}
	image.Sessions[0].AuthTime = time.Time{}
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeImage(snapshot.image, DefaultPolicyLimits())
	if err != nil || !restored.Image().Sessions[0].AuthTime.IsZero() {
		t.Fatal("unknown evidence changed after canonical recovery", err)
	}
	image.Version = 1
	encoded, _ := json.Marshal(image)
	if _, err := DecodeImage(encoded, DefaultPolicyLimits()); !errors.Is(err, ErrInvalidImage) {
		t.Fatal("old image silently migrated", err)
	}
}

func TestImageMembershipReuse(t *testing.T) {
	image := testImage()
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "other_admin"}, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	first, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := first.AccessFor(testIdentity())
	other, _ := first.AccessFor(image.Principals[1].Identity)
	if admin != other {
		t.Fatal("identical Role sets did not share a view")
	}
	image.Principals[1].State = Suspended
	next, err := compileImage(image, DefaultPolicyLimits(), first)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := next.AccessFor(testIdentity())
	if current != admin {
		t.Fatal("account-only revision rebuilt the Role view")
	}
	image.Roles[0].Rules = append(image.Roles[0].Rules, dataRule(Allow, VertexRead, "public:"))
	changed, err := compileImage(image, DefaultPolicyLimits(), next)
	if err != nil {
		t.Fatal(err)
	}
	current, _ = changed.AccessFor(testIdentity())
	if current == admin || !current.Scope(VertexRead).Contains("public:1") || admin.Scope(VertexRead).Contains("public:1") {
		t.Fatal("Role revision reused stale permission")
	}
}

func TestImageUIDBindingAndUniqueness(t *testing.T) {
	const uidA = "11111111111111111111111111111111"
	const uidB = "22222222222222222222222222222222"
	image := testImage()
	image.Roles = append(image.Roles, Role{ID: "personal", Rules: []PermissionRule{{Effect: Allow, Action: VertexRead, Resource: DataResource, PrincipalUIDPrefix: &authz.PrincipalUIDPrefix{LiteralBefore: "users:", LiteralAfter: ":"}}}})
	idA, idB := testIdentity(), testIdentity()
	idA.Subject = "a"
	idB.Subject = "b"
	image.Principals = append(image.Principals, Principal{Identity: idA, State: Active, UserUID: uidA, Assignments: []RoleAssignment{{RoleID: "personal"}}}, Principal{Identity: idB, State: Active, UserUID: uidB, Assignments: []RoleAssignment{{RoleID: "personal"}}})
	first, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := first.AccessFor(idA)
	b, _ := first.AccessFor(idB)
	if a == b || !a.Scope(VertexRead).Contains("users:"+uidA+":entry") || a.Scope(VertexRead).Contains("users:"+uidB+":entry") || b.Scope(VertexRead).Contains("users:"+uidA+":entry") {
		t.Fatal("same Role shared different UID view")
	}
	if uid, prefix, registered := first.PersonalArea(idA); !registered || uid != uidA || prefix != "users:"+uidA+":" {
		t.Fatal("trusted personal area missing")
	}
	if _, _, registered := first.PersonalArea(testIdentity()); registered {
		t.Fatal("legacy user automatically registered")
	}
	next, err := compileImage(first.Image(), DefaultPolicyLimits(), first)
	if err != nil {
		t.Fatal(err)
	}
	nextA, _ := next.AccessFor(idA)
	nextB, _ := next.AccessFor(idB)
	if nextA != a || nextB != b {
		t.Fatal("unchanged bindings failed bounded previous snapshot reuse")
	}
	changed := first.Image()
	changed.Principals[1].Assignments = nil
	withdrawn, err := compileImage(changed, DefaultPolicyLimits(), first)
	if err != nil {
		t.Fatal(err)
	}
	withdrawnA, _ := withdrawn.AccessFor(idA)
	if withdrawnA.Allows(VertexRead, "users:"+uidA+":entry") {
		t.Fatal("previous access restored withdrawn assignment")
	}
	if uid, _, registered := withdrawn.PersonalArea(idA); !registered || uid != uidA {
		t.Fatal("withdrawal lost stable UID")
	}
	encoded, _ := json.Marshal(first.Image())
	restored, err := DecodeImage(encoded, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	restoredA, _ := restored.AccessFor(idA)
	if restoredA.Allows(VertexRead, "users:"+uidB+":entry") {
		t.Fatal("recovery mixed subjects")
	}
	for _, tc := range []struct {
		name string
		edit func(*Image)
	}{
		{"invalid UID", func(i *Image) { i.Principals[1].UserUID = "BAD" }},
		{"zero UID", func(i *Image) { i.Principals[1].UserUID = strings.Repeat("0", 32) }},
		{"missing UID assignment", func(i *Image) { i.Principals[1].UserUID = "" }},
		{"duplicate UID", func(i *Image) { i.Principals[2].UserUID = uidA }},
		{"tombstone collision", func(i *Image) {
			i.Principals[2].UserUID = uidA
			i.Principals[2].State = Deleted
			i.Principals[2].Assignments = nil
		}},
		{"machine UID", func(i *Image) { i.Principals[1].Identity = Identity{Kind: MachinePrincipal, MachineName: "machine"} }},
		{"UID-less machine assignment", func(i *Image) {
			i.Principals[1].Identity = Identity{Kind: MachinePrincipal, MachineName: "machine"}
			i.Principals[1].UserUID = ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := first.Image()
			tc.edit(&candidate)
			if _, err := CompileImage(candidate, DefaultPolicyLimits()); err == nil {
				t.Fatal("invalid principal binding admitted")
			}
		})
	}
	t.Run("deletion retains UID without access", func(t *testing.T) {
		candidate := first.Image()
		if err := applyManagementChanges(&candidate, []Change{{Kind: DeletePrincipal, Identity: &idA}}); err != nil {
			t.Fatal(err)
		}
		deleted, err := CompileImage(candidate, DefaultPolicyLimits())
		if err != nil {
			t.Fatal(err)
		}
		if deleted.Image().Principals[1].UserUID != uidA {
			t.Fatal("deleted UID reusable")
		}
		if _, ok := deleted.AccessFor(idA); ok {
			t.Fatal("deleted access live")
		}
		if _, _, ok := deleted.PersonalArea(idA); ok {
			t.Fatal("deleted personal metadata disclosed")
		}
		if err := applyManagementChanges(&candidate, []Change{{Kind: PutPrincipal, Identity: &idA, State: Active}}); err == nil {
			t.Fatal("ordinary mutation resurrected user")
		}
	})
}
