package security

import "slices"

// Issuer is registered before token verification. SecretRef is a handle to an
// operator-owned binding, never a client secret or an arbitrary credential URL.
type Issuer struct {
	ConfigRevision uint64         `json:"config_revision"`
	EnvOwned       bool           `json:"env_owned,omitempty"`
	Deleted        bool           `json:"deleted,omitempty"`
	URL            string         `json:"url"`
	Enabled        bool           `json:"enabled"`
	ClientID       string         `json:"client_id"`
	APIAudience    string         `json:"api_audience"`
	RedirectURI    string         `json:"redirect_uri"`
	Algorithms     []string       `json:"algorithms"`
	SecretRef      string         `json:"secret_ref,omitempty"`
	NativeClients  []NativeClient `json:"native_clients,omitempty"`
	// The trusted issuer guarantees client subjects cannot collide with or
	// impersonate enrolled end-user subjects. Qualification is required before
	// enabling this contract; RFC 9068 alone makes no such guarantee.
	HumanSubjectNamespaceQualified bool `json:"human_subject_namespace_qualified,omitempty"`
}

// BrowserEnabled reports whether the existing browser/API profile is configured.
// A native-only issuer has none of these fields; partial browser profiles fail
// validation rather than borrowing a native audience or secret.
func (i Issuer) BrowserEnabled() bool {
	return i.ClientID != "" || i.APIAudience != "" || i.RedirectURI != "" || i.SecretRef != ""
}

func (i Issuer) clone() Issuer {
	i.Algorithms = slices.Clone(i.Algorithms)
	i.NativeClients = slices.Clone(i.NativeClients)
	for n := range i.NativeClients {
		i.NativeClients[n].Presenters = slices.Clone(i.NativeClients[n].Presenters)
	}
	return i
}

// NativeClient returns a detached registered profile. The request can select an
// ID, but cannot supply trust, provider destinations or client configuration.
func (i Issuer) NativeClient(id string) (NativeClient, bool) {
	for _, client := range i.NativeClients {
		if client.ID == id {
			client.Presenters = slices.Clone(client.Presenters)
			return client, true
		}
	}
	return NativeClient{}, false
}

// ValidateIssuerConfiguration is shared by committed-state and provider
// validation. Network discovery never repairs incomplete or ambiguous trust.
func ValidateIssuerConfiguration(i Issuer) error {
	return validateIssuerConfiguration(i, nil)
}

func validateIssuerConfiguration(i Issuer, preserveNative []string) error {
	if !validIssuerURL(i.URL) || i.Deleted && i.Enabled || len(i.Algorithms) == 0 || len(i.Algorithms) > 4 ||
		len(i.NativeClients) > MaxNativeClients || !i.BrowserEnabled() && len(i.NativeClients) == 0 {
		return ErrInvalidImage
	}
	if i.BrowserEnabled() {
		if !boundedText(i.ClientID, 512) || !boundedText(i.APIAudience, 512) ||
			len(i.SecretRef) > 64 || i.SecretRef != "" && !validRoleID(i.SecretRef) {
			return ErrInvalidImage
		}
		if _, ok := validHTTPSURL(i.RedirectURI); !ok {
			return ErrInvalidImage
		}
	} else if i.HumanSubjectNamespaceQualified {
		return ErrInvalidImage
	}
	for n, algorithm := range i.Algorithms {
		switch algorithm {
		case "RS256", "PS256", "ES256", "EdDSA":
		default:
			return ErrInvalidImage
		}
		if slices.Contains(i.Algorithms[:n], algorithm) {
			return ErrInvalidImage
		}
	}
	for n, client := range i.NativeClients {
		if !client.validConfiguration(i.URL, slices.Contains(preserveNative, client.ID)) || n > 0 && i.NativeClients[n-1].ID >= client.ID || !slices.Contains(i.Algorithms, "RS256") {
			return ErrInvalidImage
		}
	}
	return nil
}
