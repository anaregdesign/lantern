package oidc

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/server/internal/security"

	"github.com/anaregdesign/lantern/core/privatefile"
)

// SecretBinding is operator configuration, never management API input. The
// name is the only reference persisted in replicated Issuer metadata.
type SecretBinding struct {
	Issuer      string `json:"issuer"`
	ClientID    string `json:"client_id"`
	TokenOrigin string `json:"token_origin"`
	Path        string `json:"path"`
	Value       string `json:"value,omitempty"`
}

type SecretRegistry struct{ bindings map[string]SecretBinding }

func NewSecretRegistry(bindings map[string]SecretBinding) (*SecretRegistry, error) {
	if len(bindings) > 64 {
		return nil, ErrFetch
	}
	r := &SecretRegistry{bindings: make(map[string]SecretBinding, len(bindings))}
	for name, binding := range bindings {
		u, err := endpointURL(binding.TokenOrigin)
		issuerURL, issuerErr := endpointURL(binding.Issuer)
		if !validSecretName(name) || err != nil ||
			binding.TokenOrigin != endpointOrigin(u) || issuerErr != nil || issuerURL.RawQuery != "" ||
			binding.ClientID == "" || len(binding.ClientID) > 512 ||
			binding.Path == "" && !validSecretValue(binding.Value) ||
			binding.Path != "" && (!filepath.IsAbs(binding.Path) || binding.Value != "") {
			return nil, ErrFetch
		}
		r.bindings[name] = binding
	}
	return r, nil
}

func (r *SecretRegistry) load(issuer security.Issuer, tokenEndpoint string) (string, error) {
	return r.loadClient(issuer.URL, issuer.ClientID, issuer.SecretRef, tokenEndpoint)
}

func (r *SecretRegistry) loadClient(issuer, clientID, secretRef, tokenEndpoint string) (string, error) {
	if r == nil {
		return "", ErrFetch
	}
	endpoint, err := endpointURL(tokenEndpoint)
	binding, known := r.bindings[secretRef]
	if err != nil || !known || binding.Issuer != issuer || binding.ClientID != clientID ||
		binding.TokenOrigin != endpointOrigin(endpoint) {
		return "", ErrFetch
	}
	if binding.Path == "" {
		if !validSecretValue(binding.Value) {
			return "", ErrFetch
		}
		return binding.Value, nil
	}
	before, err := os.Lstat(binding.Path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 4096 {
		return "", ErrFetch
	}
	file, err := os.Open(binding.Path)
	if err != nil {
		return "", ErrFetch
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || privatefile.Check(file) != nil {
		return "", ErrFetch
	}
	value, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || !validSecretValue(string(value)) {
		return "", ErrFetch
	}
	return string(value), nil
}

func validSecretValue(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func validSecretName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
