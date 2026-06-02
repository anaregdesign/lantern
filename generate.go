// Package lantern roots repository-wide `go generate` directives that don't
// belong to a single module. Buf lives here because the generated protobuf
// stubs land inside the sdks/go module while the .proto sources sit at the
// repo root — neither is an obvious home for the codegen, so a workspace-root
// directive keeps it discoverable.
//
// The wire directive lives in server/generate.go, alongside the module that
// owns the `tool github.com/google/wire/cmd/wire` declaration.
//
// Run everything with:
//
//	go generate ./...
package lantern

//go:generate go run github.com/bufbuild/buf/cmd/buf@v1.70.0 generate
