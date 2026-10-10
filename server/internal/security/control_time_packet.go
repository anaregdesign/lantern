package security

import (
	"encoding/binary"
	"errors"
)

var (
	errAuthorityTimeSourceFault = errors.New("time source reports unusable time")
	errAuthorityTimeRate        = errors.New("time source requests backoff")
	errAuthorityTimeDenied      = errors.New("time source denies this association")
)

// Fixed-header SNTP observation. Fields named reported are untrusted server
// claims. Interpreting them as conservative bounds requires the source premise.
type authorityNTPPacket struct {
	reference, receive, transmit uint64
	reportedRootDelay            int32
	reportedRootDispersion       uint32
	reportedStratum              byte
	reportedReference            [4]byte
	reportedPrecision            int8
}

func parseAuthorityNTP(raw []byte, nonce [8]byte) (authorityNTPPacket, error) {
	// This producer supports only a 48-byte, LI=0, VN=4, mode=server packet.
	// Leap warnings, KoD, unsynchronized strata, MACs and extensions all refuse.
	if len(raw) != 48 || raw[0]&63 != 4<<3|4 || nonce == [8]byte{} {
		return authorityNTPPacket{}, errAuthorityTime
	}
	if [8]byte(raw[24:32]) != nonce {
		return authorityNTPPacket{}, errAuthorityTime
	}
	if raw[1] == 0 && string(raw[12:16]) == "RATE" {
		return authorityNTPPacket{}, errAuthorityTimeRate
	}
	if raw[1] == 0 && (string(raw[12:16]) == "DENY" || string(raw[12:16]) == "RSTR") {
		return authorityNTPPacket{}, errAuthorityTimeDenied
	}
	if raw[0]>>6 != 0 || raw[1] == 0 || raw[1] > 15 {
		return authorityNTPPacket{}, errAuthorityTimeSourceFault
	}
	p := authorityNTPPacket{
		reference: binary.BigEndian.Uint64(raw[16:24]),
		receive:   binary.BigEndian.Uint64(raw[32:40]), transmit: binary.BigEndian.Uint64(raw[40:48]),
		reportedRootDelay:      int32(binary.BigEndian.Uint32(raw[4:8])),
		reportedRootDispersion: binary.BigEndian.Uint32(raw[8:12]),
		reportedStratum:        raw[1], reportedReference: [4]byte(raw[12:16]),
		reportedPrecision: int8(raw[3]),
	}
	if p.reference == 0 || p.receive == 0 || p.transmit == 0 || p.reportedRootDelay < 0 {
		return authorityNTPPacket{}, errAuthorityTimeSourceFault
	}
	return p, nil
}
