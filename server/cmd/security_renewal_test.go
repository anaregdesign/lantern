package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestSecurityRenewalArguments(t *testing.T) {
	options, help, err := parseSecurityRenewal([]string{"--operator-directory", "/private/retained-operator", "--output", "/private/new-renewal"})
	if err != nil || help || options.OutputDirectory != "/private/new-renewal" || options.OperatorDirectory != "/private/retained-operator" || options.ValidFor != 30*24*time.Hour {
		t.Fatal("renewal arguments lost the retained/new directory distinction", err)
	}
	options, help, err = parseSecurityRenewal([]string{"--operator-directory=/private/retained-operator", "--output=/private/new-renewal", "--publisher-config=/private/previous-renewal/publisher.json", "--valid-for=48h"})
	if err != nil || help || options.ValidFor != 48*time.Hour || options.PublisherConfig != "/private/previous-renewal/publisher.json" {
		t.Fatal("explicit current publisher config or renewal window rejected", err)
	}
	for _, args := range [][]string{
		nil,
		{"--output", "/private/new-renewal"},
		{"--operator-directory", "/private/retained-operator"},
		{"--operator-directory", "/private/retained-operator", "--output", "/private/new-renewal", "unexpected"},
		{"--operator-directory", "/private/retained-operator", "--output", "/private/new-renewal", "--valid-for=0"},
		{"--operator-directory", "/private/retained-operator", "--output", "/private/new-renewal", "--valid-for=-1h"},
		{"--operator-directory", "/private/retained-operator", "--output", "/private/new-renewal", "--valid-for=secret-invalid-duration"},
		{"--ca-key=secret-invalid-argument"},
	} {
		if _, _, err := parseSecurityRenewal(args); err == nil {
			t.Fatal("ambiguous renewal arguments accepted")
		} else if strings.Contains(err.Error(), "secret-invalid") {
			t.Fatal("renewal argument error disclosed private text")
		}
	}
}

func TestSecurityRenewalHelpHasNoEnvironmentDependency(t *testing.T) {
	t.Setenv("LANTERN_AUTH_MODE", "invalid")
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", "private-help-sentinel")
	for _, arg := range []string{"-h", "--help"} {
		var out bytes.Buffer
		if err := renewSecurity(context.Background(), []string{arg}, &out); err != nil {
			t.Fatal("help attempted renewal or runtime configuration", err)
		}
		for _, text := range []string{"publication.json", "COMPLETE", "CLEAN", "advance-window", "publisher-config", "unchanged trust and state", "not overwritten"} {
			if !strings.Contains(out.String(), text) {
				t.Fatal("missing maintenance requirement in help", text)
			}
		}
		if strings.Contains(out.String(), "private-help-sentinel") {
			t.Fatal("help disclosed process configuration")
		}
	}
}
