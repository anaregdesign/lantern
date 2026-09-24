# lantern-sdk-python (archived)

The former gRPC Python SDK was archived on 2026-06-06. Its source and
historical tags (`sdks/python/v0.1.x`) remain here for reference, but the SDK
is no longer built, tested, or published. The existing PyPI `lantern-sdk`
v0.1.x releases use that historical gRPC implementation; they are not a
maintained Connect client.

[#355](https://github.com/anaregdesign/lantern/issues/355) tracks a new Python
SDK only if both upstream Connect Python reaches a documented stable 1.x
contract and a Lantern user identifies a concrete Python workflow. As of
2026-09-24, upstream [`connectrpc`](https://pypi.org/project/connectrpc/)
is still classified Beta. This interim HTTP+JSON example does not revive the
archived SDK.

## Interim usage: Connect HTTP+JSON

Lantern serves Connect, gRPC, and gRPC-Web on one socket, `:6380` by default
(`LANTERN_PORT` changes it). There is no separate Connect listener. A Python
HTTP client can make unary Connect calls by POSTing JSON to
`/{service}/{method}` with `Content-Type: application/json`. The example below
uses the standard library; it needs no generated code or package installation.
It is an unauthenticated local h2c fixture; a production application must use
HTTPS and application-owned authentication.

Start Lantern from the repository root in one terminal:

```bash
go run ./server/cmd
```

In another terminal, run the [executable
example](examples/connect_json_smoke.py):

```bash
python3 sdks/python.archived/examples/connect_json_smoke.py \
  --endpoint http://127.0.0.1:6380
```

The example sends `PutVertex`, `GetVertex`, and `Illuminate` through the real
Connect handler, verifies a missing Vertex returns `not_found`, and deletes its
unique synthetic key. Its request shapes are:

```json
{"vertex":{"key":"<unique-key>","string":"archive-smoke"}}
```

```json
{"seed":"<unique-key>","bfs":{"step":1,"fanOut":1}}
```

`Illuminate` is unary. Its `bfs` family arm is required, and `fanOut` is the
protobuf JSON name of `BfsParams.fan_out`; the retired flat `step`/`k` fields
are invalid. `Vertex.string` is a oneof field directly on `Vertex`, not under
an additional `value` object. A missing `GetVertex` returns an HTTP 404
Connect error with JSON code `not_found`.

### Streaming RPCs

`BackupSnapshot` on `graph.v1.LanternService`, and `Subscribe`/`Snapshot` on
`graph.v1.LanternReplicationService`, are **server-streaming** RPCs. Their
Connect responses contain length-prefixed envelopes rather than one JSON
document. Use a Connect streaming client or implement the framing and
end-of-stream semantics from the [Connect protocol
reference](https://connectrpc.com/docs/protocol#streaming-response).
`Subscribe` and `Snapshot` are replication interfaces for peers; they are not
the interim Python application's cache API. See #1116 for the later
client-facing identity-only change stream.

### Schema and upstream tooling

The canonical schemas are
[`proto/graph/v1/graph.proto`](../../proto/graph/v1/graph.proto) and
[`proto/graph/v1/replication.proto`](../../proto/graph/v1/replication.proto).
The executable example is exercised against a real in-process Connect server
by `go test ./tests/integration -run TestPythonArchiveHTTPJSONFixture`; that
gate checks the current request shape and a missing-key failure on every Go
CI run.

If the conditions in #355 later justify a maintained typed SDK, use upstream
[`connectrpc/connect-py`](https://github.com/connectrpc/connect-py), the
`connectrpc` runtime, and its current Buf remote plugin
`buf.build/connectrpc/py` (or local `protoc-gen-connectrpc`). Start a new
package rather than importing the generated gRPC files in this archive.

## Historical content

The archived `src/`, `tests/`, `pyproject.toml`, and old `examples/quickstart.py`
remain unchanged and are not wired into the workspace's build or release.
Only the standalone HTTP+JSON documentation fixture above is exercised.
The archive decision is recorded in
[#341](https://github.com/anaregdesign/lantern/issues/341) and the parent
Connect-only migration epic
[#335](https://github.com/anaregdesign/lantern/issues/335).
