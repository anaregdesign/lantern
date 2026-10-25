package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestSecurityProvisioningArguments(t *testing.T) {
	options, help, err := parseSecurityProvisioning([]string{"--output", "/private/new-trust", "--operator-directory", "/private/new-operator"})
	if err != nil || help || options.OutputDirectory != "/private/new-trust" || options.OperatorDirectory != "/private/new-operator" || options.ValidFor != 30*24*time.Hour {
		t.Fatal("provisioning options did not preserve explicit custody choices", err)
	}
	options, help, err = parseSecurityProvisioning([]string{"--output=/private/trust", "--operator-directory=/private/operator", "--valid-for=48h"})
	if err != nil || help || options.ValidFor != 48*time.Hour {
		t.Fatal("explicit operator window rejected", err)
	}
	for _, args := range [][]string{
		nil,
		{"--output", "/private/trust"},
		{"--operator-directory", "/private/operator"},
		{"--output", "/private/trust", "--operator-directory", "/private/operator", "unexpected"},
		{"--output", "/private/trust", "--operator-directory", "/private/operator", "--valid-for=0"},
		{"--output", "/private/trust", "--operator-directory", "/private/operator", "--valid-for=-1h"},
		{"--output", "/private/trust", "--operator-directory", "/private/operator", "--valid-for=secret-invalid-duration"},
		{"--client-secret=secret-invalid-argument"},
	} {
		if _, _, err := parseSecurityProvisioning(args); err == nil {
			t.Fatal("ambiguous provisioning arguments accepted")
		} else if strings.Contains(err.Error(), "secret-invalid") {
			t.Fatal("argument error disclosed supplied private text")
		}
	}
}

func TestSecurityProvisioningHelpHasNoEnvironmentDependency(t *testing.T) {
	t.Setenv("LANTERN_AUTH_MODE", "invalid")
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", "private-help-sentinel")
	for _, arg := range []string{"-h", "--help"} {
		var out bytes.Buffer
		if err := provisionSecurity(context.Background(), []string{arg}, &out); err != nil {
			t.Fatal("help tried to configure or provision the runtime", err)
		}
		for _, text := range []string{"target_platform", "fixed node topology", "init-security", "never overwritten", "operator-directory"} {
			if !strings.Contains(out.String(), text) {
				t.Fatal("missing provisioning lifecycle help", text)
			}
		}
		if strings.Contains(out.String(), "private-help-sentinel") {
			t.Fatal("help disclosed process configuration")
		}
	}
	var out bytes.Buffer
	if err := writeServerHelp(&out); err != nil || !strings.Contains(out.String(), "provision-security") || !strings.Contains(out.String(), "renew-security") || !strings.Contains(out.String(), "provisioned_file") {
		t.Fatal("missing startup selection and commands", err)
	}
}
