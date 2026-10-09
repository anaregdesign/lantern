package listenerlaunch

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestListenerLaunchProtocol(t *testing.T) {
	nonce := strings.Repeat("ab", 32)
	var wire bytes.Buffer
	want := Message{Phase: "reserve", Nonce: nonce, Private: true}
	if err := Write(&wire, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(bufio.NewReaderSize(&wire, MaxFrame))
	if err != nil || !got.Control("reserve", nonce) || !got.Private || got.Control("configure", nonce) || got.Control("reserve", strings.Repeat("cd", 32)) {
		t.Fatal("phase or child binding", err)
	}
	for _, raw := range []string{
		`{"phase":"reserve","nonce":"` + nonce + `","foreign":1}` + "\n",
		`{"phase":"reserve","phase":"configure","nonce":"` + nonce + `"}` + "\n",
		`{"phase":"reserve","nonce":"` + nonce + `"} {}` + "\n",
		`{"phase":"reserve","nonce":"bad"}` + "\n",
		strings.Repeat("x", MaxFrame) + "\n",
		`{"phase":"reserve","nonce":"` + nonce + `"}`,
	} {
		if _, err := Read(bufio.NewReaderSize(strings.NewReader(raw), MaxFrame)); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	// encoding/json recognizes case aliases for struct fields. Reject both
	// alias-only fields and aliases appended after a canonical field.
	canonical := `{"phase":"reserve","nonce":"` + nonce + `","pid":1,"private":true,"public":"[::]:1234","peer":"127.0.0.1:5678","environment":["LANTERN_PORT=1234"]}`
	if _, err := Read(bufio.NewReaderSize(strings.NewReader(canonical+"\n"), MaxFrame)); err != nil {
		t.Fatal("canonical fields rejected", err)
	}
	for _, field := range []struct{ key, alias, value string }{
		{"phase", "Phase", `"reserve"`},
		{"nonce", "Nonce", `"` + nonce + `"`},
		{"pid", "PID", `1`},
		{"private", "Private", `true`},
		{"public", "Public", `"[::]:1234"`},
		{"peer", "Peer", `"127.0.0.1:5678"`},
		{"environment", "Environment", `["LANTERN_PORT=1234"]`},
	} {
		t.Run(field.alias, func(t *testing.T) {
			for _, raw := range []string{
				strings.Replace(canonical, `"`+field.key+`":`, `"`+field.alias+`":`, 1),
				strings.TrimSuffix(canonical, "}") + `,"` + field.alias + `":` + field.value + `}`,
			} {
				if _, err := Read(bufio.NewReaderSize(strings.NewReader(raw+"\n"), MaxFrame)); err == nil {
					t.Fatal("case alias accepted")
				}
			}
		})
	}
	for _, env := range [][]string{nil, {"PATH=foreign"}, {"LANTERN_PORT=1", "LANTERN_PORT=2"}, {"LANTERN_PORT=1\x00"}, {"lantern_port=1"}} {
		if (Message{Phase: "configure", Nonce: nonce, Environment: env}).Configuration(nonce) {
			t.Fatal("invalid launch environment")
		}
	}
	if !(Message{Phase: "configure", Nonce: nonce, Environment: []string{"LANTERN_PORT=1234"}}).Configuration(nonce) {
		t.Fatal("valid configuration rejected")
	}
}
