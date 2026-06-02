// Package pb is a thin module that ships the generated protobuf and gRPC
// stubs for the Lantern wire format. It sits at the bottom of the dependency
// graph so the server and the client SDK both depend on the same contract
// without depending on each other.
package pb
