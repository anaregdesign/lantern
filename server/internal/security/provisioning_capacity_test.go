package security

import "testing"

func TestProvisioningCapacityReservesAllFixedVoters(t *testing.T) {
	for count := 1; count <= 31; count++ {
		options := provisioningTestOptions(t, count, "linux")
		bundle, err := GenerateProvisioning(options)
		if err != nil {
			t.Fatal("coherent production defaults do not support fixed voter count", count, err)
		}
		d := provisioningTestDocument(t, bundle.Nodes[0].AuthorityJSON)
		maximum := min(d.Genesis.Bounds.PayloadBytes, uint64(s2cMessageOverhead)+d.Genesis.Bounds.ProofBytes+d.Genesis.Bounds.HistoricalBytes)
		if d.Participant.OutboxBytes/uint64(count) < maximum || d.Limits.QueueBytes < d.Participant.OutboxBytes+uint64(count)*s3aQueueOverhead ||
			d.Limits.TransitionCount < count+1 || d.Limits.MaxConnections < count || d.Participant.OutboxBytes > 256<<20 || d.Limits.QueueBytes > 1<<30 {
			t.Fatal("fixed topology cannot reserve one maximum broadcast", count)
		}
	}
}
