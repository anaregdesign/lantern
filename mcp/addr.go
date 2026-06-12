package mcp

import "strings"

// parseLanternAddrs splits a comma-separated LANTERN_ADDR value into a
// clean list of endpoint URLs. Whitespace around each entry is trimmed and
// empty entries are dropped, so " a , ,b ," yields ["a","b"]. A single
// address (the common case) returns a one-element slice, preserving the
// original single-endpoint behaviour.
//
// The parsed list is handed to client.NewLanternFailover (sdks/go): a
// one-element slice yields a plain single-endpoint client, a multi-element
// slice yields the SDK's static-endpoint failover wrapper. The comma
// contract is MCP's config surface and lives here; the failover policy
// itself lives in the SDK (#592/#593).
func parseLanternAddrs(raw string) []string {
	parts := strings.Split(raw, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			addrs = append(addrs, s)
		}
	}
	return addrs
}
