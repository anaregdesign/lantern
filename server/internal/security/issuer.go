package security

// Issuer is registered before token verification. SecretRef is a handle to an
// operator-owned binding, never a client secret or an arbitrary credential URL.
type Issuer struct {
	URL         string   `json:"url"`
	Enabled     bool     `json:"enabled"`
	ClientID    string   `json:"client_id"`
	APIAudience string   `json:"api_audience"`
	RedirectURI string   `json:"redirect_uri"`
	Algorithms  []string `json:"algorithms"`
	SecretRef   string   `json:"secret_ref,omitempty"`
}
