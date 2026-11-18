//go:build !unix

package main

import "os"

func openFixtureFailureReport() (*os.File, error) {
	return nil, os.ErrInvalid
}
