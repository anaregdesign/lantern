// Package server hosts the gRPC server module. The wire generate directive
// lives here (rather than in the repo-root generate.go) so that `go generate`
// resolves the `go tool wire` directive against this module's own tool
// directive in go.mod.
//
//	go generate ./...   # from anywhere in the workspace
package server

//go:generate go tool wire ./cmd
