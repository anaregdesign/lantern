package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFixtureFailureCategory(t *testing.T) {
	private := errors.New("private credential and key content")
	for _, stage := range []string{"spawn", "readiness", "exit", "private credential"} {
		failure := &fixtureFailure{stage: stage, cause: private}
		wrapped := fmt.Errorf("wrapper: %w", failure)
		category := fixtureFailureCategory(wrapped)
		if strings.Contains(category, "private") || strings.Contains(failure.Error(), "credential") || !errors.Is(wrapped, private) {
			t.Fatal("public category exposed arbitrary data or lost its private cause")
		}
		if stage == "private credential" && category != "configuration" || stage != "private credential" && category != stage {
			t.Fatal("wrong fixed category", category)
		}
	}
	if fixtureFailureCategory(private) != "configuration" {
		t.Fatal("arbitrary error changed the category")
	}
}
