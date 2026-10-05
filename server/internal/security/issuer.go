package security

// Issuer is registered before token verification. SecretRef is a handle to an
// operator-owned binding, never a client secret or an arbitrary credential URL.
type Issuer struct {
	ConfigRevision uint64   `json:"config_revision"`
	EnvOwned       bool     `json:"env_owned,omitempty"`
	Deleted        bool     `json:"deleted,omitempty"`
	URL            string   `json:"url"`
	Enabled        bool     `json:"enabled"`
	ClientID       string   `json:"client_id"`
	APIAudience    string   `json:"api_audience"`
	RedirectURI    string   `json:"redirect_uri"`
	Algorithms     []string `json:"algorithms"`
	SecretRef      string   `json:"secret_ref,omitempty"`
	// The trusted issuer guarantees client subjects cannot collide with or
	// impersonate enrolled end-user subjects. Qualification is required before
	// enabling this contract; RFC 9068 alone makes no such guarantee.
	HumanSubjectNamespaceQualified bool `json:"human_subject_namespace_qualified,omitempty"`
}
