package provider

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/service"
	"os"
	"strings"
)

// ChangeConfig is public CDC configuration; it never enables private Subscribe.
type ChangeConfig struct {
	Enabled bool
	Options service.ChangeServiceOptions
}

func loadChangeConfig(auth SecurityConfig, peer PeerPlaneConfig) (ChangeConfig, error) {
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
		key := service.ChangeCursorKey{Version: 1}
		if _, err := rand.Read(key.Key[:]); err != nil {
			return ChangeConfig{}, err
		}
		cfg.Options.CursorKeys, cfg.Options.CurrentKeyVersion = []service.ChangeCursorKey{key}, 1
		return cfg, nil
	}
	raw, err := readSecurityOperatorFile(file, true, 4096)
	if err != nil {
		return ChangeConfig{}, err
	}
	var ring struct {
		Current uint32 `json:"current_version"`
		Keys    []struct {
			Version uint32 `json:"version"`
			Key     string `json:"key"`
		} `json:"keys"`
	}
	if err := strictSecurityConfigJSON(string(raw), &ring); err != nil || ring.Current == 0 || len(ring.Keys) == 0 || len(ring.Keys) > 4 {
		return ChangeConfig{}, errors.New("invalid CDC cursor key ring")
	}
	seen := make(map[uint32]bool)
	for _, source := range ring.Keys {
		key, err := hex.DecodeString(source.Key)
		if err != nil || len(key) != 32 || source.Key != hex.EncodeToString(key) || source.Version == 0 || seen[source.Version] {
			return ChangeConfig{}, errors.New("invalid CDC cursor key")
		}
		item := service.ChangeCursorKey{Version: source.Version}
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
