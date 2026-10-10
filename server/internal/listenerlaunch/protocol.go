package listenerlaunch

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
)

const Argument = "--native-fixture-listeners"
const MaxFrame = 64 << 10

// Message is a bounded local pipe protocol, never a network request. Each
// phase is bound to the nonce supplied to this exact child at process creation.
// Configuration contains only existing LANTERN environment settings. It cannot
// assert runtime certification. PID/addresses are reservation metadata only.
type Message struct {
	Phase       string   `json:"phase"`
	Nonce       string   `json:"nonce"`
	PID         int      `json:"pid,omitempty"`
	Private     bool     `json:"private,omitempty"`
	Public      string   `json:"public,omitempty"`
	Peer        string   `json:"peer,omitempty"`
	Environment []string `json:"environment,omitempty"`
}

func ValidNonce(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == s && s != strings.Repeat("0", 64)
}

func Write(out io.Writer, m Message) error {
	raw, err := json.Marshal(m)
	if err != nil || len(raw) >= MaxFrame || !ValidNonce(m.Nonce) {
		return ErrLaunch
	}
	raw = append(raw, '\n')
	n, err := out.Write(raw)
	if err == nil && n != len(raw) {
		return io.ErrShortWrite
	}
	return err
}

func Read(in *bufio.Reader) (Message, error) {
	var result Message
	// ReadSlice, unlike ReadString, cannot accumulate an unbounded frame.
	raw, err := in.ReadSlice('\n')
	if err != nil || len(raw) > MaxFrame {
		return result, ErrLaunch
	}
	// Reject duplicate top-level keys as well as unknown fields. All structured
	// values here are typed scalars or a flat string list, never nested objects.
	keys := json.NewDecoder(bytes.NewReader(raw))
	token, err := keys.Token()
	if err != nil || token != json.Delim('{') {
		return result, ErrLaunch
	}
	seen := map[string]bool{}
	for keys.More() {
		token, err = keys.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return result, ErrLaunch
		}
		// The typed decoder also recognizes case-insensitive aliases. Admit
		// only canonical spellings before its duplicate/field handling.
		switch key {
		case "phase", "nonce", "pid", "private", "public", "peer", "environment":
		default:
			return result, ErrLaunch
		}
		seen[key] = true
		var value json.RawMessage
		if keys.Decode(&value) != nil {
			return result, ErrLaunch
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || !ValidNonce(result.Nonce) {
		return Message{}, ErrLaunch
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Message{}, ErrLaunch
	}
	return result, nil
}

func (m Message) Control(phase, nonce string) bool {
	return m.Phase == phase && m.Nonce == nonce && m.PID == 0 && m.Public == "" && m.Peer == "" && len(m.Environment) == 0
}

func (m Message) Configuration(nonce string) bool {
	if m.Phase != "configure" || m.Nonce != nonce || m.PID != 0 || m.Private || m.Public != "" || m.Peer != "" || len(m.Environment) == 0 || len(m.Environment) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, entry := range m.Environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "LANTERN_") || strings.ToUpper(key) != key || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
