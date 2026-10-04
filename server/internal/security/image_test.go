package security

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
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
