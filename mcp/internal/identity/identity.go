// Package identity resolves the stable agent id every working-context
// tool call is attributed to (#851).
//
// Resolution order:
//
//  1. LANTERN_MCP_AGENT_ID — the operator-owned identity. Fleet operators
//     own uniqueness: two sessions sharing an id last-writer-win on the
//     agents.<id> presence vertex (accepted and documented, not defended).
//  2. Auto-generated stable fallback "<hostname>-<pid>-<rand4>", computed
//     once per process so every tool call in a session attributes to the
//     same agent.
//
// There is deliberately no per-call id parameter in v1 — a spoofable
// request-level identity would be weaker than the env-level one, and a
// fleet shares one trust domain anyway (transport auth is #850).
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
)

var (
	once     sync.Once
	resolved string
)

// Resolve returns the agent id for this process. The first call decides;
// later calls return the same value even if the environment changed, so a
// session can never change identity mid-flight.
func Resolve() string {
	once.Do(func() { resolved = resolve(os.Getenv("LANTERN_MCP_AGENT_ID")) })
	return resolved
}

// resolve is the pure core, separated for tests (the package-level
// Resolve memoises; tests call resolve directly with crafted inputs).
func resolve(env string) string {
	if id := sanitize(env); id != "" {
		return id
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "agent"
	}
	var buf [2]byte
	_, _ = rand.Read(buf[:]) // rand.Read never fails on supported platforms
	return fmt.Sprintf("%s-%d-%s", sanitize(host), os.Getpid(), hex.EncodeToString(buf[:]))
}

// sanitize makes an identity fragment safe for use inside a dotted
// Lantern key: whitespace and dots collapse to '-' (dots would create
// phantom key segments under the agents. prefix), and surrounding
// junk is trimmed. Empty in, empty out.
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '.' || r == ' ' || r == '\t' || r == '\n':
			return '-'
		default:
			return r
		}
	}, s)
}
