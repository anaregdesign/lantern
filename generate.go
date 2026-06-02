// Package lantern roots a few repository-wide `go generate` directives so that
// `go generate ./...` regenerates everything codegen-related without anyone
// having to install extra CLIs first.
//
//   - wire is pulled in via the `tool` directive in go.mod, so `go tool wire`
//     just works after `go mod download`.
//   - buf is invoked through `go run` against a pinned version so contributors
//     don't need a system-wide `buf` binary either. CI uses
//     bufbuild/buf-setup-action which is faster, but the `go run` fallback
//     keeps local dev one-command.
//
// Run everything with:
//
//	go generate ./...
package lantern

//go:generate go tool wire ./server/cmd
//go:generate go run github.com/bufbuild/buf/cmd/buf@v1.70.0 generate --clean
