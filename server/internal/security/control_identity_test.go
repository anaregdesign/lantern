package security

import (
	"os"
	"testing"
)

func TestS3AIdentityRejectsBeforeAnyStateCreation(t *testing.T) {
	for _, name := range []string{"scope", "voter-key", "proposer-role", "mapping", "voting-key-file", "root-file", "tls-key-file", "san", "spki", "self", "extra-voter", "paths"} {
		t.Run(name, func(t *testing.T) {
			n := s3aTestCluster(t, nil)
			c := n.configs[1]
			c.Membership.Profile.Voters = append(c.Membership.Profile.Voters[:0:0], c.Membership.Profile.Voters...)
			switch name {
			case "scope":
				c.Membership.Profile.ProtocolScope[0]++
			case "voter-key":
				c.Membership.Profile.Voters[0].Key[0]++
			case "proposer-role":
				c.Membership.Profile.Voters[0].Proposer = false
			case "mapping":
				c.Membership.Profile.Voters[0].Voter = 99
			case "voting-key-file":
				c.Identity.VotingKey = n.configs[2].Identity.VotingKey
			case "root-file":
				c.Membership.Profile.TrustDigest[0]++
			case "tls-key-file":
				c.Identity.TLSKey = n.configs[2].Identity.TLSKey
			case "san":
				c.Identity.Certificate = n.configs[2].Identity.Certificate
				c.Identity.TLSKey = n.configs[2].Identity.TLSKey
			case "spki":
				c.Membership.Profile.Voters[0].Workload.SPKI[0]++
				c.Membership.Self = c.Membership.Profile.Voters[0].Workload
			case "self":
				c.Membership.Self = n.configs[2].Membership.Self
			case "extra-voter":
				c.Membership.Profile.Voters = append(c.Membership.Profile.Voters, c.Membership.Profile.Voters[0])
			case "paths":
				c.Membership.Path = c.Participant.PPath
			}
			if owner, err := createS3AOwner(c); err == nil {
				_ = owner.Close()
				t.Fatal("unqualified identity opened")
			}
			for _, path := range []string{n.configs[1].Membership.Path, c.Participant.PPath, c.Participant.BPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("rejection created durable family", path, err)
				}
			}
		})
	}
}
