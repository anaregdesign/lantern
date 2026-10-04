# Server data namespace and original mutation evidence

Implementation slices for #1615, #1603, #1612 and #1613, stacked on #1627.
Production OIDC activation, management/browser mounts, private workload listener,
scoped CDC and final qualification remain separate prerequisites in #1599.

An explicitly certified `namespaced-v1` serving runtime stores every public
logical identity as `data:` plus that identity exactly once. Public literal keys
beginning with `sys:` or `data:` are ordinary distinct data keys. Prefixes, seeds,
Edge endpoints, missing results, cursors, search and public exports use logical
identities. Values and opaque credential/receipt bytes are never namespace-mapped.
Typed `sys:` images remain outside graph mutations, search statistics and public
export. Internal apply, WAL and private snapshots operate on physical identities.

Server admission captures one immutable policy cut before lookup or effect.
Exact mixed-denied batches fail before any partial effect or existence disclosure.
Collection queries translate logical Allow-minus-Deny scope to generic immutable
Core ranges; candidates are constrained before limits/counts/top-k and traversal
never crosses denied Nodes/Edges. Ranking statistics remain shared within the
existing corpus. Scan/key/count operations require `vertex.read`; Query is an
independent search/traversal action. No per-Role ranking-statistics rebuild occurs.

Lifetime reduction is decided from the same atomic application-time sample as
storage, including conditional and born-expired Put, and requires the explicit
Delete action. Receipt reservation captures original opaque logical resources
and the actual lifecycle effect. Replay/status recheck current read, receipt and
original mutation permissions before disclosing any original result; unproven
legacy/unknown evidence is not a scoped wildcard. Original Add effective float32
bytes, including non-finite accumulated results, remain authoritative.

The physical format is explicit on retained mutations, peer Subscribe/Snapshot/
PeerStatus and whole-state recovery. Unknown or mismatched formats fail before
publication, never from guessing a key's spelling. Receipt WAL, baseline/archive
and snapshot fingerprints advance together with original provenance. Earlier
unsupported frames are rejected rather than accepted with invented evidence.
Native receipt snapshot versioning remains independent of Server wire framing.
This slice exposes the certified runtime seam; the production environment loader
still retains its staged OIDC guard until remaining serving dependencies exist.

Paired tests cover exact action checks, range/count separation, logical/physical
round trips, format rejection, original resource/effect authorization and atomic
restore. Root integration exercises real Connect mapping, literal reserved-looking
keys, public sys exclusion, mixed invalid-batch rollback and receipt replay.
Generated Go, Node, Dart and Rust consumers come from the same proto sources.
