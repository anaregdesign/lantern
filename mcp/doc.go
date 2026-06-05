// Package mcp hosts the lantern-mcp binary and its supporting packages.
//
// lantern-mcp is a Model Context Protocol (MCP) server that exposes a remote
// Lantern gRPC instance as decaying graph memory for LLM agents. It is a
// standalone executable, not a library: import paths under this module are
// intentionally internal-leaning.
//
// Dependency boundary: this module imports only the public Lantern Go
// surface — github.com/anaregdesign/lantern/pb and
// github.com/anaregdesign/lantern/sdks/go — never the server or core
// packages. The MCP server talks to Lantern over the wire like any other
// external client.
package mcp
