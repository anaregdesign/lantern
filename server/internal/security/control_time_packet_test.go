package security

import "testing"

func TestAuthorityNTPPacket(t *testing.T) {
	nonce := [8]byte{1, 2, 3}
	base := fakeAuthorityNTPResponse(nonce)
	if _, err := parseAuthorityNTP(base[:], nonce); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func([]byte) []byte{
		"short":           func(b []byte) []byte { return b[:47] },
		"extension":       func(b []byte) []byte { return append(b, 0) },
		"wrong mode":      func(b []byte) []byte { b[0] = 4<<3 | 3; return b },
		"old version":     func(b []byte) []byte { b[0] = 3<<3 | 4; return b },
		"leap insertion":  func(b []byte) []byte { b[0] |= 1 << 6; return b },
		"leap deletion":   func(b []byte) []byte { b[0] |= 2 << 6; return b },
		"unsynchronized":  func(b []byte) []byte { b[0] |= 3 << 6; return b },
		"KoD":             func(b []byte) []byte { b[1] = 0; copy(b[12:16], "RATE"); return b },
		"invalid stratum": func(b []byte) []byte { b[1] = 16; return b },
		"wrong nonce":     func(b []byte) []byte { b[24] ^= 1; return b },
		"zero receive":    func(b []byte) []byte { clear(b[32:40]); return b },
		"zero reference":  func(b []byte) []byte { clear(b[16:24]); return b },
		"negative delay":  func(b []byte) []byte { b[4] = 128; return b },
		"zero transmit":   func(b []byte) []byte { clear(b[40:48]); return b },
	} {
		t.Run(name, func(t *testing.T) {
			b := append([]byte(nil), base[:]...)
			if _, err := parseAuthorityNTP(change(b), nonce); err == nil {
				t.Fatal("invalid packet accepted")
			}
		})
	}
	for _, code := range []string{"RATE", "DENY", "RSTR"} {
		raw := base
		raw[1] = 0
		copy(raw[12:16], code)
		_, err := parseAuthorityNTP(raw[:], nonce)
		want := errAuthorityTimeDenied
		if code == "RATE" {
			want = errAuthorityTimeRate
		}
		if err != want {
			t.Fatalf("KoD %s: %v", code, err)
		}
	}
	if _, err := parseAuthorityNTP(base[:], [8]byte{}); err == nil {
		t.Fatal("zero nonce accepted")
	}
	base[4], base[8], base[12] = 127, 255, 255
	p, err := parseAuthorityNTP(base[:], nonce)
	if err != nil || p.reportedRootDelay != 0x7f000000 || p.reportedRootDispersion != 0xff000000 || p.reportedReference[0] != 255 {
		t.Fatalf("report not preserved: %+v %v", p, err)
	}
}
