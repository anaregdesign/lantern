package security

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"reflect"
	"testing"
	"time"
)

func TestRenewProvisioningIdentityPreservesImmutableTrust(t *testing.T) {
	for _, target := range []string{"linux", "darwin"} {
		t.Run(target, func(t *testing.T) {
			options := provisioningTestOptions(t, 3, target)
			bundle, err := GenerateProvisioning(options)
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range bundle.Nodes {
				original := provisioningTestDocument(t, node.AuthorityJSON)
				oldBlock, _ := pem.Decode(node.CertificatePEM)
				oldLeaf, err := x509.ParseCertificate(oldBlock.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				// The signed membership and old leaf may both have expired. This
				// offline operation proves identity only and grants no live lease.
				now := oldLeaf.NotAfter.Add(time.Hour)
				cleanup := now.Add(2 * time.Hour)
				raw, certificate, err := RenewProvisioningIdentity(bundle.Operator.RootsPEM, bundle.Operator.CAKeyPEM, node.AuthorityJSON, now, cleanup, target)
				if err != nil {
					t.Fatal("explicit maintenance rejected coherent expired original", err)
				}
				renewed := provisioningTestDocument(t, raw)
				if renewed.Identity.Certificate != string(certificate) || bytes.Equal(certificate, node.CertificatePEM) {
					t.Fatal("certificate renewal did not update exact output")
				}
				renewed.Identity.Certificate = original.Identity.Certificate
				if !reflect.DeepEqual(original, renewed) {
					t.Fatal("certificate maintenance changed immutable trust or a private key")
				}
				block, _ := pem.Decode(certificate)
				leaf, err := x509.ParseCertificate(block.Bytes)
				if err != nil || leaf.SerialNumber.Cmp(oldLeaf.SerialNumber) == 0 || !leaf.NotAfter.After(oldLeaf.NotAfter) ||
					!bytes.Equal(leaf.RawSubjectPublicKeyInfo, oldLeaf.RawSubjectPublicKeyInfo) ||
					sha256.Sum256(leaf.RawSubjectPublicKeyInfo) != original.Membership.Self.SPKI ||
					!reflect.DeepEqual(leaf.URIs, oldLeaf.URIs) || !reflect.DeepEqual(leaf.DNSNames, oldLeaf.DNSNames) ||
					!reflect.DeepEqual(leaf.IPAddresses, oldLeaf.IPAddresses) || !reflect.DeepEqual(leaf.ExtKeyUsage, oldLeaf.ExtKeyUsage) {
					t.Fatal("renewed certificate changed fixed identity or did not extend capacity", err)
				}
				roots := x509.NewCertPool()
				if !roots.AppendCertsFromPEM(bundle.Operator.RootsPEM) {
					t.Fatal("missing root")
				}
				for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
					if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: cleanup, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
						t.Fatal("renewed chain does not cover declared cleanup", err)
					}
				}
			}
		})
	}
}

func TestRenewProvisioningIdentityRejectsChangedTrustAndKeys(t *testing.T) {
	options := provisioningTestOptions(t, 2, "linux")
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateProvisioning(provisioningTestOptions(t, 1, "linux"))
	if err != nil {
		t.Fatal(err)
	}
	node := bundle.Nodes[0]
	now := options.Now.Add(7 * 24 * time.Hour)
	cleanup := now.Add(2 * time.Hour)
	for name, change := range map[string]func(*currentJSONProvisioningDocument){
		"old TLS key": func(d *currentJSONProvisioningDocument) {
			d.Identity.TLSKey = string(bundle.Nodes[1].TLSKeyPEM)
		},
		"old certificate": func(d *currentJSONProvisioningDocument) {
			d.Identity.Certificate = string(bundle.Nodes[1].CertificatePEM)
		},
		"stored roots": func(d *currentJSONProvisioningDocument) {
			d.Identity.Roots = string(other.Operator.RootsPEM)
		},
		"time profile": func(d *currentJSONProvisioningDocument) {
			d.Genesis.TimeProfile[0] ^= 1
		},
		"voting key": func(d *currentJSONProvisioningDocument) {
			d.Identity.VotingKey[0] ^= 1
		},
		"origin key": func(d *currentJSONProvisioningDocument) {
			d.OriginKey[0] ^= 1
		},
		"workload SPKI": func(d *currentJSONProvisioningDocument) {
			d.Membership.Self.SPKI[0] ^= 1
		},
		"workload URI": func(d *currentJSONProvisioningDocument) {
			d.Membership.Self.Identity += "-changed"
		},
		"genesis voting key": func(d *currentJSONProvisioningDocument) {
			d.Genesis.Members[0].PublicKey[0] ^= 1
		},
		"participant enrollment": func(d *currentJSONProvisioningDocument) {
			d.Participant.Incarnation[0] ^= 1
		},
		"membership public key": func(d *currentJSONProvisioningDocument) {
			d.Membership.PublicKey = nil
		},
		"invalid capacity": func(d *currentJSONProvisioningDocument) {
			d.Limits.QueueBytes = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := provisioningTestDocument(t, node.AuthorityJSON)
			change(&d)
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			updated, certificate, err := RenewProvisioningIdentity(bundle.Operator.RootsPEM, bundle.Operator.CAKeyPEM, raw, now, cleanup, "linux")
			if err == nil || updated != nil || certificate != nil {
				t.Fatal("changed identity produced a maintenance certificate")
			}
		})
	}
	for _, input := range []struct {
		name         string
		roots, caKey []byte
		target       string
		cleanup      time.Time
	}{
		{"wrong root", other.Operator.RootsPEM, bundle.Operator.CAKeyPEM, "linux", cleanup},
		{"wrong CA key", bundle.Operator.RootsPEM, other.Operator.CAKeyPEM, "linux", cleanup},
		{"leaf key as CA", bundle.Operator.RootsPEM, node.TLSKeyPEM, "linux", cleanup},
		{"missing key", bundle.Operator.RootsPEM, nil, "linux", cleanup},
		{"different target", bundle.Operator.RootsPEM, bundle.Operator.CAKeyPEM, "darwin", cleanup},
		{"unknown target", bundle.Operator.RootsPEM, bundle.Operator.CAKeyPEM, "host", cleanup},
		{"beyond root", bundle.Operator.RootsPEM, bundle.Operator.CAKeyPEM, "linux", now.AddDate(11, 0, 0)},
		{"no extension", bundle.Operator.RootsPEM, bundle.Operator.CAKeyPEM, "linux", options.CleanupBy},
	} {
		t.Run(input.name, func(t *testing.T) {
			updated, certificate, err := RenewProvisioningIdentity(input.roots, input.caKey, node.AuthorityJSON, now, input.cleanup, input.target)
			if err == nil || updated != nil || certificate != nil {
				t.Fatal("invalid maintenance request produced new serving material")
			}
		})
	}
}
