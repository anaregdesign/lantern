/**
 * Additive-edge idempotency keys (contrib IDs) for the Node SDK.
 *
 * A contrib ID is the 24-byte identifier the server uses to make an
 * additive `AddEdge` / `AddEdges` contribution idempotent: re-sending the
 * same bytes (e.g. a transport retry) is a no-op instead of double-counting
 * the edge weight. The layout mirrors the Go SDK and the server's
 * `contribIDFor` exactly, so ids minted by either SDK are interchangeable:
 *
 *   bytes [0:16] = per-client nonce (origin)
 *   bytes [16:24] = uint64 big-endian = (seq << 16) | (index & 0xffff)
 *
 * Two ways to get an id onto the wire (see {@link EdgeInput.contribId} and
 * `ConnectOptions.idempotentAdds`):
 *
 *   - Opt-in automatic ids: the client mints `(nonce, seq, index)` triples,
 *     one `seq` per Add call (per chunk), so a retried call re-sends the
 *     same bytes.
 *   - Caller-supplied deterministic ids: pass an exactly-24-byte
 *     `contribId` per edge to control the dedup key yourself.
 *
 * Dedup horizon: dedup only holds while the contribution is live. Once the
 * edge decays past its TTL (or is deleted) the id is forgotten, so a later
 * Add with the same id contributes weight again. Contrib IDs guard retries
 * within a contribution's lifetime, not for all time.
 */

import { InvalidArgumentError } from "./errors.js";

/** Wire length of a contrib ID, in bytes. Must match the server. */
export const CONTRIB_ID_BYTES = 24;

/** Length of the per-client nonce that occupies the id's high 16 bytes. */
const NONCE_BYTES = 16;

/**
 * Validate a caller-supplied contrib ID. The server treats any byte string
 * whose length is not exactly {@link CONTRIB_ID_BYTES} as absent (the legacy
 * additive path), so a wrong length is almost always a silent bug — reject it
 * eagerly instead.
 */
export function validateContribId(id: Uint8Array): Uint8Array {
  if (id.length !== CONTRIB_ID_BYTES) {
    throw new InvalidArgumentError(
      `contribId must be exactly ${CONTRIB_ID_BYTES} bytes, got ${id.length}`,
    );
  }
  return id;
}

/**
 * Mint a fresh per-client nonce (16 cryptographically-random bytes) that
 * seeds the high half of every automatic contrib ID. Uses Web Crypto
 * (`crypto.getRandomValues`), available on Node 20+ and in browsers, so no
 * Node-only import leaks into the web bundle.
 */
export function makeNonce(): Uint8Array {
  const nonce = new Uint8Array(NONCE_BYTES);
  crypto.getRandomValues(nonce);
  return nonce;
}

/**
 * Build the 24-byte automatic contrib ID for `(nonce, seq, index)`. `seq`
 * is a per-call monotonic counter and `index` is the edge's position within
 * its batch chunk; folding the index into the low 16 bits lets one chunk
 * carry up to 65 536 distinct ids under a single seq while staying
 * byte-identical to the server's `contribIDFor`.
 */
export function contribIdFrom(nonce: Uint8Array, seq: bigint, index: number): Uint8Array {
  const id = new Uint8Array(CONTRIB_ID_BYTES);
  id.set(nonce.subarray(0, NONCE_BYTES), 0);
  const combined = BigInt.asUintN(64, (seq << 16n) | BigInt(index & 0xffff));
  new DataView(id.buffer, id.byteOffset, id.byteLength).setBigUint64(NONCE_BYTES, combined, false);
  return id;
}
