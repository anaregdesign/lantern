package mcp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/value"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// Working-context key scheme (#851). Dotted, consistent with the lintKey
// conventions the memory profile established:
//
//	agents.<id>          presence heartbeat (task line, TTL=transient)
//	claims.<resource>    advisory lease on <resource> (TTL=turn)
//	notes.<id>           blackboard note (explicit bucket)
//	<anything else>      a resource — caller-chosen dotted key
//	                     (e.g. repo.lantern.core.graphcache)
//
// Prefix scans give every listing for free and TTL expiry makes each one
// self-cleaning. When the legacy memory profile ran against the same
// server the two key populations share one keyspace; switching profiles
// does NOT migrate or clean old data — TTL decay handles it.
const (
	agentKeyPrefix = "agents."
	claimKeyPrefix = "claims."
	noteKeyPrefix  = "notes."
)

func agentKey(id string) string       { return agentKeyPrefix + id }
func claimKey(resource string) string { return claimKeyPrefix + resource }

// contextKind classifies a vertex key into the working-context buckets
// whats_happening reports. Anything outside the three reserved prefixes
// is a resource by definition.
func contextKind(key string) string {
	switch {
	case strings.HasPrefix(key, agentKeyPrefix):
		return "agent"
	case strings.HasPrefix(key, claimKeyPrefix):
		return "claim"
	case strings.HasPrefix(key, noteKeyPrefix):
		return "note"
	default:
		return "resource"
	}
}

// presenceRecord is the JSON payload stored at agents.<id>.
type presenceRecord struct {
	Task   string `json:"task"`
	Detail string `json:"detail,omitempty"`
	Since  string `json:"since,omitempty"` // RFC3339; first announce of this task line
}

// claimRecord is the JSON payload stored at claims.<resource>.
type claimRecord struct {
	Holder string `json:"holder"`
	Note   string `json:"note,omitempty"`
	At     string `json:"at,omitempty"` // RFC3339; when the lease was (re)granted
}

// noteRecord is the JSON payload stored at notes.<id>.
type noteRecord struct {
	Text     string   `json:"text"`
	Author   string   `json:"author"`
	Severity string   `json:"severity,omitempty"`
	Links    []string `json:"links,omitempty"`
}

// encodeRecord marshals a context payload for PutVertex. JSON-in-string
// keeps the wire value a plain string vertex, so the memory-profile tools
// (and Admin) render it legibly if they ever meet it.
func encodeRecord(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeRecord unmarshals a context payload from a fetched vertex into
// out. Returns false when the vertex does not hold a JSON string of the
// expected shape (foreign data sharing the keyspace) — callers surface
// such entries raw instead of erroring.
func decodeRecord(v *client.Vertex, out any) bool {
	s := value.Text(v)
	if s == "" {
		return false
	}
	return json.Unmarshal([]byte(s), out) == nil
}

// expirationIn converts a resolved TTL duration into the absolute
// expiration client.EdgeInput carries.
func expirationIn(d time.Duration) time.Time { return time.Now().Add(d) }
