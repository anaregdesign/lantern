package provider

import (
	"context"
	"strings"
	"testing"
)

func TestInitializeSecurityRequiresExplicitEnvironmentProvisioning(t *testing.T) {
	for _, config := range []SecurityConfig{{Mode: "off"}, {Mode: "oidc", ConfigFile: "/operator/node.json", StoreMode: "fresh", BrowserOrigin: "https://admin.example"}} {
		if err := InitializeSecurity(context.Background(), config); err == nil || !strings.Contains(err.Error(), "environment JSON") {
			t.Fatal("initialization accepted an implicit or legacy source", err)
		}
	}
}
