package main

import "errors"

// Only fixed categories cross a fixture's public startup boundary.
type fixtureFailure struct {
	stage string
	cause error
}

func (f *fixtureFailure) Error() string { return "fixture failed: " + fixtureFailureCategory(f) }
func (f *fixtureFailure) Unwrap() error { return f.cause }

func fixtureFailureCategory(err error) string {
	var failure *fixtureFailure
	if errors.As(err, &failure) {
		switch failure.stage {
		case "configuration", "generation", "spawn", "readiness", "publication", "exit", "private_input":
			return failure.stage
		}
	}
	return "configuration"
}
