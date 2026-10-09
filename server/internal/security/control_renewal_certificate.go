package security

import (
	"bytes"
	"encoding/binary"
	"sort"
)

const authorityRenewalCertificateDomain = "lantern/security/current-renewal/certificate\x00\x01"
const authorityRenewalMaxCertificate = len(authorityRenewalCertificateDomain) + 4 + len(authorityRenewalRequestDomain) + authorityRenewalMaxStatement + 64 + 1 + 31*68

// Historical certificate data is not a live receiver capability. A live
// receiver additionally retains its own unexported native challenge sample.
type authorityRenewalCertificate struct {
	request authorityRenewalRequest
	votes   []authorityRenewalVote
	raw     string
}

func encodeAuthorityRenewalCertificate(requestRaw []byte, votes []authorityRenewalVote, trust *s2cTrust, workloads [32]byte) (authorityRenewalCertificate, error) {
	if trust == nil || len(votes) < trust.majority() || len(votes) > len(trust.members) {
		return authorityRenewalCertificate{}, errAuthorityRenewal
	}
	request, err := parseAuthorityRenewalRequest(requestRaw, trust, workloads)
	if err != nil {
		return authorityRenewalCertificate{}, err
	}
	votes = append([]authorityRenewalVote(nil), votes...)
	sort.Slice(votes, func(i, j int) bool { return votes[i].member < votes[j].member })
	out := append([]byte(authorityRenewalCertificateDomain), binary.BigEndian.AppendUint32(nil, uint32(len(requestRaw)))...)
	out = append(out, requestRaw...)
	out = append(out, byte(len(votes)))
	var previous uint32
	for _, v := range votes {
		if v.member <= previous {
			return authorityRenewalCertificate{}, errAuthorityRenewal
		}
		raw := append(binary.BigEndian.AppendUint32(nil, v.member), v.signature[:]...)
		if _, err := parseAuthorityRenewalVote(raw, request, trust); err != nil {
			return authorityRenewalCertificate{}, err
		}
		out = append(out, raw...)
		previous = v.member
	}
	return authorityRenewalCertificate{request, votes, string(out)}, nil
}

func parseAuthorityRenewalCertificate(raw []byte, trust *s2cTrust, workloads [32]byte) (authorityRenewalCertificate, error) {
	if trust == nil || len(raw) < len(authorityRenewalCertificateDomain)+5 || len(raw) > authorityRenewalMaxCertificate || !bytes.HasPrefix(raw, []byte(authorityRenewalCertificateDomain)) {
		return authorityRenewalCertificate{}, errAuthorityRenewal
	}
	body := raw[len(authorityRenewalCertificateDomain):]
	n := uint64(binary.BigEndian.Uint32(body[:4]))
	body = body[4:]
	if n > uint64(len(body)-1) || n > uint64(len(authorityRenewalRequestDomain)+authorityRenewalMaxStatement+64) {
		return authorityRenewalCertificate{}, errAuthorityRenewal
	}
	requestRaw := body[:n]
	body = body[n:]
	count := int(body[0])
	body = body[1:]
	if count < trust.majority() || count > len(trust.members) || len(body) != count*68 {
		return authorityRenewalCertificate{}, errAuthorityRenewal
	}
	request, err := parseAuthorityRenewalRequest(requestRaw, trust, workloads)
	if err != nil {
		return authorityRenewalCertificate{}, err
	}
	votes := make([]authorityRenewalVote, 0, count)
	var previous uint32
	for len(body) > 0 {
		v, err := parseAuthorityRenewalVote(body[:68], request, trust)
		if err != nil || v.member <= previous {
			return authorityRenewalCertificate{}, errAuthorityRenewal
		}
		votes = append(votes, v)
		previous = v.member
		body = body[68:]
	}
	return authorityRenewalCertificate{request, votes, string(raw)}, nil
}
