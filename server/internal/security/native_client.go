package security

import "strings"

const (
	MaxNativeClients    = 16
	MaxNativePresenters = 16
	GoogleIDToken       = "google_id_token"
	AppleNative         = "apple_native"
	AppleAndroidWeb     = "apple_android_web"
	GoogleIssuer        = "https://accounts.google.com"
	AppleIssuer         = "https://appleid.apple.com"
)

// NativeClient is committed operator trust, not a client-supplied OAuth request.
// SecretRef names a server-private binding; it contains neither a path nor JWT.
type NativeClient struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	ClientID       string   `json:"client_id"`
	ServiceOrigin  string   `json:"service_origin"`
	Presenters     []string `json:"presenters,omitempty"`
	RedirectURI    string   `json:"redirect_uri,omitempty"`
	AndroidPackage string   `json:"android_package,omitempty"`
	SecretRef      string   `json:"secret_ref,omitempty"`
}

func (c NativeClient) valid(issuer string) bool {
	return c.validConfiguration(issuer, false)
}

func (c NativeClient) validConfiguration(issuer string, preserveSecret bool) bool {
	if !validRoleID(c.ID) || len(c.ID) > 64 || !boundedText(c.ClientID, 512) || strings.ContainsAny(c.ClientID, "* \t\r\n") ||
		!nativeServiceOrigin(c.ServiceOrigin) || len(c.Presenters) > MaxNativePresenters {
		return false
	}
	switch c.Kind {
	case GoogleIDToken:
		if issuer != GoogleIssuer || c.RedirectURI != "" || c.AndroidPackage != "" || c.SecretRef != "" {
			return false
		}
		for n, presenter := range c.Presenters {
			if !boundedText(presenter, 512) || strings.ContainsAny(presenter, "* \t\r\n") || n > 0 && c.Presenters[n-1] >= presenter {
				return false
			}
		}
	case AppleNative, AppleAndroidWeb:
		if issuer != AppleIssuer || len(c.Presenters) != 0 || !(preserveSecret && c.SecretRef == "") && !validRoleID(c.SecretRef) || len(c.SecretRef) > 64 {
			return false
		}
		if c.Kind == AppleNative {
			return c.RedirectURI == "" && c.AndroidPackage == ""
		}
		return c.RedirectURI == c.ServiceOrigin+"/auth/native/apple/callback/"+c.ID && nativeAndroidPackage(c.AndroidPackage)
	default:
		return false
	}
	return true
}

func nativeServiceOrigin(origin string) bool {
	u, ok := validHTTPSURL(origin)
	return ok && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.String() == origin && strings.ToLower(u.Host) == u.Host
}

func nativeAndroidPackage(value string) bool {
	if len(value) > 255 || !strings.Contains(value, ".") {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for n, r := range part {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || n > 0 && r >= '0' && r <= '9' {
				continue
			}
			return false
		}
	}
	return true
}
