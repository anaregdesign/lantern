package security

import "testing"

func TestNativeClientAppleProfiles(t *testing.T) {
	native := NativeClient{ID: "ios", Kind: AppleNative, ClientID: "com.example.native", ServiceOrigin: "https://lantern.example", SecretRef: "apple-native"}
	web := NativeClient{ID: "android", Kind: AppleAndroidWeb, ClientID: "com.example.services", ServiceOrigin: "https://lantern.example", SecretRef: "apple-web", AndroidPackage: "com.example.app", RedirectURI: "https://lantern.example/auth/native/apple/callback/android"}
	if !native.valid(AppleIssuer) || !web.valid(AppleIssuer) {
		t.Fatal("valid profiles rejected")
	}
	for _, tc := range []struct {
		name   string
		client NativeClient
		mutate func(*NativeClient)
	}{
		{"native redirect", native, func(c *NativeClient) { c.RedirectURI = web.RedirectURI }},
		{"web wrong callback", web, func(c *NativeClient) { c.RedirectURI += "?next=evil" }},
		{"web wrong package", web, func(c *NativeClient) { c.AndroidPackage = "com.example;evil" }},
		{"missing secret", native, func(c *NativeClient) { c.SecretRef = "" }},
		{"private path", native, func(c *NativeClient) { c.SecretRef = "/private/key" }},
		{"HTTP", native, func(c *NativeClient) { c.ServiceOrigin = "http://lantern.example" }},
		{"query origin", native, func(c *NativeClient) { c.ServiceOrigin += "?" }},
		{"path origin", native, func(c *NativeClient) { c.ServiceOrigin += "/" }},
		{"userinfo origin", native, func(c *NativeClient) { c.ServiceOrigin = "https://user@lantern.example" }},
		{"presenter", native, func(c *NativeClient) { c.Presenters = []string{"client"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.client
			tc.mutate(&c)
			if c.valid(AppleIssuer) {
				t.Fatal("invalid profile accepted")
			}
		})
	}
}
