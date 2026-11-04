package provider

import (
	"strconv"
	"strings"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

// OperationalBootstrapSource describes how the loaded effective input arose.
// Its zero value is unknown, never an invented default for hand-built Configs.
type OperationalBootstrapSource string

const (
	OperationalBootstrapDefault     OperationalBootstrapSource = "default"
	OperationalBootstrapEnvironment OperationalBootstrapSource = "environment"
	OperationalBootstrapDerived     OperationalBootstrapSource = "derived"
)

type OperationalBootstrapField struct {
	Key    string
	Source OperationalBootstrapSource
}

// OperationalBootstrapConfig retains only bounded provenance from the same
// configuration load. It contains no Reader, raw environment values, findings,
// secrets or mutable maps. Actual runtime owners separately report effective
// values/errors and may mark a constructor-normalized field derived.
type OperationalBootstrapConfig struct {
	captured bool
	sources  [43]OperationalBootstrapSource
}

var operationalBootstrapFields = [43]struct {
	key   string
	group operationalpolicy.Group
}{
	{"LANTERN_SLOW_RPC_THRESHOLD_MS", operationalpolicy.GroupSlowRPC},
	{"LANTERN_RATE_LIMIT_RPS", operationalpolicy.GroupRate},
	{"LANTERN_RATE_LIMIT_BURST", operationalpolicy.GroupRate},
	{"LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT", operationalpolicy.GroupRequest},
	{"LANTERN_DELETE_BY_PREFIX_MAX_LIMIT", operationalpolicy.GroupRequest},
	{"LANTERN_ILLUMINATE_MAX_K", operationalpolicy.GroupRequest},
	{"LANTERN_ILLUMINATE_MAX_STEP", operationalpolicy.GroupRequest},
	{"LANTERN_MAX_BATCH_SIZE", operationalpolicy.GroupRequest},
	{"LANTERN_MAX_KEY_LEN", operationalpolicy.GroupRequest},
	{"LANTERN_SCAN_DEFAULT_LIMIT", operationalpolicy.GroupRequest},
	{"LANTERN_SCAN_MAX_LIMIT", operationalpolicy.GroupRequest},
	{"LANTERN_TRAVERSAL_MAX_PUSHES", operationalpolicy.GroupRequest},
	{"LANTERN_TRAVERSAL_MAX_RESULTS", operationalpolicy.GroupRequest},
	{"LANTERN_TRAVERSAL_MAX_TOUCHED_EDGES", operationalpolicy.GroupRequest},
	{"LANTERN_TRAVERSAL_TIMEOUT_MS", operationalpolicy.GroupRequest},
	{"LANTERN_BACKUP_ENABLED", operationalpolicy.GroupBackup},
	{"LANTERN_BACKUP_INTERVAL", operationalpolicy.GroupBackup},
	{"LANTERN_BACKUP_RETAIN", operationalpolicy.GroupBackup},
	{"LANTERN_GC_EDGE_BUDGET", operationalpolicy.GroupCapacity},
	{"LANTERN_GC_INTERVAL_SECONDS", operationalpolicy.GroupCapacity},
	{"LANTERN_MAX_EDGES", operationalpolicy.GroupCapacity},
	{"LANTERN_MAX_EDGE_CAUSAL_ENTRIES", operationalpolicy.GroupCapacity},
	{"LANTERN_MAX_VERTEX_CAUSAL_ENTRIES", operationalpolicy.GroupCapacity},
	{"LANTERN_MAX_VERTICES", operationalpolicy.GroupCapacity},
	{"LANTERN_DRAIN_DELAY_SECONDS", operationalpolicy.GroupLifecycle},
	{"LANTERN_MAX_REPLICATION_LAG", operationalpolicy.GroupLifecycle},
	{"LANTERN_SHUTDOWN_TIMEOUT_SECONDS", operationalpolicy.GroupLifecycle},
	{"LANTERN_SEARCH_DEFAULT_LIMIT", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_MAX_LIMIT", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_DEFAULT_MODE", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_DEFAULT_MIN_SHOULD", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_TIMEOUT_MS", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_MAX_QUERY_BYTES", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_MAX_QUERY_TERMS", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_MAX_DICTIONARY_VISITS", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_MAX_POSTING_VISITS", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_MAX_POSITION_VISITS", operationalpolicy.GroupSearch},
	{"LANTERN_SEARCH_MAX_EXPIRATION_VISITS", operationalpolicy.GroupSearch},
	{"LANTERN_ANTI_ENTROPY_GAP_WARN_THRESHOLD", operationalpolicy.GroupReplication},
	{"LANTERN_ANTI_ENTROPY_INTERVAL_MS", operationalpolicy.GroupReplication},
	{"LANTERN_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS", operationalpolicy.GroupReplication},
	{"LANTERN_PUMP_BACKOFF_MAX_MS", operationalpolicy.GroupReplication},
	{"LANTERN_PUMP_BACKOFF_MIN_MS", operationalpolicy.GroupReplication},
}

// NewOperationalBootstrapConfig projects the captured provenance. Runtime
// providers consume this focused value rather than the aggregate Config.
func NewOperationalBootstrapConfig(c *Config) OperationalBootstrapConfig {
	if c == nil {
		return OperationalBootstrapConfig{}
	}
	return c.operationalBootstrap
}

func (c OperationalBootstrapConfig) Source(key string) (OperationalBootstrapSource, bool) {
	if !c.captured {
		return "", false
	}
	for index, field := range operationalBootstrapFields {
		if field.key == key {
			return c.sources[index], c.sources[index] != ""
		}
	}
	return "", false
}

// Fields returns a detached closed group projection, never a mutable reference
// to metadata retained by the initial load.
func (c OperationalBootstrapConfig) Fields(group operationalpolicy.Group) ([]OperationalBootstrapField, bool) {
	if !c.captured || !group.Valid() {
		return nil, false
	}
	var fields []OperationalBootstrapField
	for index, field := range operationalBootstrapFields {
		if field.group == group {
			if c.sources[index] == "" {
				return nil, false
			}
			fields = append(fields, OperationalBootstrapField{Key: field.key, Source: c.sources[index]})
		}
	}
	return fields, true
}

func captureOperationalBootstrap(reader *envconfig.Reader, rate RateLimitConfig, production backup.Config) OperationalBootstrapConfig {
	diagnostics := reader.Diagnostics()
	kinds := make(map[string]string, len(diagnostics.Known))
	for _, spec := range diagnostics.Known {
		kinds[spec.Key] = spec.Kind
	}
	malformed := make(map[string]bool, len(diagnostics.Findings))
	for _, finding := range diagnostics.Findings {
		malformed[finding.Key] = true
	}
	result := OperationalBootstrapConfig{captured: true}
	for index, field := range operationalBootstrapFields {
		kind, registered := kinds[field.key]
		if !registered {
			result.captured = false
			return result
		}
		raw, present := reader.LookupEnv(field.key)
		source := OperationalBootstrapDefault
		if present && (kind == "string" || strings.TrimSpace(raw) != "") && !malformed[field.key] {
			source = OperationalBootstrapEnvironment
		}
		switch field.key {
		case "LANTERN_RATE_LIMIT_BURST":
			// The primitive default is computed from RPS. Preserve the
			// dependency even for an invalid NaN/Inf input; the actual owner
			// reports that error instead of fabricating default values.
			if source == OperationalBootstrapDefault && rate.RPS != 0 {
				source = OperationalBootstrapDerived
			}
			if source == OperationalBootstrapEnvironment && rate.RPS > 0 {
				if explicit, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && explicit <= 0 {
					source = OperationalBootstrapDerived
				}
			}
		case "LANTERN_BACKUP_ENABLED":
			if source == OperationalBootstrapEnvironment && !production.Enabled {
				switch strings.ToLower(strings.TrimSpace(raw)) {
				case "1", "true", "yes", "on":
					source = OperationalBootstrapDerived
				}
			}
		}
		result.sources[index] = source
	}
	return result
}
