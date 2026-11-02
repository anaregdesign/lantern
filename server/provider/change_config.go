package provider

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"os"
	"strings"
)

// ChangeConfig is public CDC configuration; it never enables private Subscribe.
type ChangeConfig struct {
	Enabled bool
	Options dataruntime.ChangeServiceOptions
}

func loadChangeConfig(auth SecurityConfig, peer PeerPlaneConfig) (ChangeConfig, error) {
	if len(auth.AuthorityJSON) != 0 {
		return loadJSONChangeConfig(auth.CDC)
	}
	enabledText := envconfig.String("LANTERN_CDC_ENABLED", "true")
	file := envconfig.String("LANTERN_CDC_CURSOR_KEY_RING_FILE", "")
	if enabledText != "true" && enabledText != "false" {
		return ChangeConfig{}, errors.New("CDC enabled must be exactly true or false")
	}
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(strings.ToUpper(name), "LANTERN_CDC_") && name != "LANTERN_CDC_ENABLED" && name != "LANTERN_CDC_CURSOR_KEY_RING_FILE" {
			return ChangeConfig{}, errors.New("unknown CDC configuration setting")
		}
	}
	if enabledText == "false" {
		if file != "" {
			return ChangeConfig{}, errors.New("disabled CDC cannot ignore cursor keys")
		}
		return ChangeConfig{}, nil
	}
	cfg := ChangeConfig{Enabled: true}
	if file == "" {
		if auth.Mode == "oidc" || peer.ListenAddress != "" {
			return ChangeConfig{}, errors.New("protected or HA CDC requires shared operator-owned cursor keys")
		}
		key := dataruntime.ChangeCursorKey{Version: 1}
		if _, err := rand.Read(key.Key[:]); err != nil {
			return ChangeConfig{}, err
		}
		cfg.Options.CursorKeys, cfg.Options.CurrentKeyVersion = []dataruntime.ChangeCursorKey{key}, 1
		return cfg, nil
	}
	raw, err := readSecurityOperatorFile(file, true, 4096)
	if err != nil {
		return ChangeConfig{}, err
	}
	return changeConfigFromKeyRing(raw)
}

type changeCursorKeyRingInput struct {
	Current uint32 `json:"current_version"`
	Keys    []struct {
		Version uint32 `json:"version"`
		Key     string `json:"key"`
	} `json:"keys"`
}

func changeConfigFromKeyRing(raw []byte) (ChangeConfig, error) {
	var ring changeCursorKeyRingInput
	if err := strictSecurityConfigJSON(string(raw), &ring); err != nil || ring.Current == 0 || len(ring.Keys) == 0 || len(ring.Keys) > 4 {
		return ChangeConfig{}, errors.New("invalid CDC cursor key ring")
	}
	cfg := ChangeConfig{Enabled: true}
	seen := make(map[uint32]bool)
	for _, source := range ring.Keys {
		key, err := hex.DecodeString(source.Key)
		if err != nil || len(key) != 32 || source.Key != hex.EncodeToString(key) || source.Version == 0 || seen[source.Version] {
			return ChangeConfig{}, errors.New("invalid CDC cursor key")
		}
		item := dataruntime.ChangeCursorKey{Version: source.Version}
		copy(item.Key[:], key)
		if item.Key == [32]byte{} {
			return ChangeConfig{}, errors.New("zero CDC cursor key")
		}
		cfg.Options.CursorKeys = append(cfg.Options.CursorKeys, item)
		seen[source.Version] = true
	}
	if !seen[ring.Current] {
		return ChangeConfig{}, errors.New("missing current CDC cursor key")
	}
	cfg.Options.CurrentKeyVersion = ring.Current
	return cfg, nil
}

// CDC is explicit opt-in for environment JSON. Its keys share the secret
// injection boundary; no independent file setting or precedence is accepted.
func loadJSONChangeConfig(raw json.RawMessage) (ChangeConfig, error) {
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(strings.ToUpper(name), "LANTERN_CDC_") {
			return ChangeConfig{}, errors.New("security JSON cannot be combined with CDC environment settings")
		}
	}
	if len(raw) == 0 {
		return ChangeConfig{}, nil
	}
	var input struct {
		Enabled bool `json:"enabled"`
		changeCursorKeyRingInput
	}
	if err := strictSecurityConfigJSON(string(raw), &input); err != nil {
		return ChangeConfig{}, errors.New("invalid security JSON CDC configuration")
	}
	if !input.Enabled {
		if input.Current != 0 || len(input.Keys) != 0 {
			return ChangeConfig{}, errors.New("disabled CDC cannot ignore cursor keys")
		}
		return ChangeConfig{}, nil
	}
	ring, err := json.Marshal(input.changeCursorKeyRingInput)
	if err != nil {
		return ChangeConfig{}, errors.New("invalid security JSON CDC key ring")
	}
	return changeConfigFromKeyRing(ring)
}

type publicChangeMetrics struct{ metrics *domainmetrics.DomainMetrics }

func (m publicChangeMetrics) OnSubscribeStarted()              { m.metrics.OnChangeStarted() }
func (m publicChangeMetrics) OnSubscribeEnded()                { m.metrics.OnChangeEnded() }
func (m publicChangeMetrics) OnSubscribeDropped(reason string) { m.metrics.OnChangeDropped(reason) }
func NewChangeConfig(cfg *Config, metrics *domainmetrics.DomainMetrics) ChangeConfig {
	changes := cfg.Changes
	if metrics != nil {
		changes.Options.Metrics = publicChangeMetrics{metrics}
	}
	return changes
}
