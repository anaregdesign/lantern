# Changelog

## 0.1.0

- Scaffold the pure-Dart `lantern_client` package.
- Add reproducible Connect-Dart and Protobuf code generation.
- Expose the generated `Vertex`, `Edge`, and `Graph` value types.
- Add the secure, injectable `LanternClient` transport foundation with
  per-call token providers, deadlines, cancellation, typed errors, and close.
- Add auth-exempt gRPC Health-v1 `ping()` probing with typed non-serving status
  errors.
