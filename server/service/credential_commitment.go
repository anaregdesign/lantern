package service

import "crypto/sha256"

func requestCredentialCommitment(domain, raw string) [32]byte {
	return sha256.Sum256([]byte("lantern/authentication/" + domain + "/v1\x00" + raw))
}
