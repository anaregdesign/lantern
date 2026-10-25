package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestInitializeSecurityRefusesAnonymousMode(t *testing.T) {
	t.Setenv("LANTERN_AUTH_MODE", "off")
	if err := initializeSecurity(context.Background()); err == nil {
		t.Fatal("OFF mode initialized protected state")
	}
}

func TestInitializeSecurityRejectsExtraArguments(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"lantern", "init-security", "--typo"}
	if err := runMain(); err == nil || !strings.Contains(err.Error(), "does not accept arguments") {
		t.Fatal("initialization accepted extra arguments", err)
	}
}
