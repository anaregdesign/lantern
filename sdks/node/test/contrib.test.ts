/**
 * Unit tests for the contrib-ID helpers (#895). These assert the exact
 * 24-byte wire layout so an id minted here stays byte-identical to the Go
 * SDK and the server's `contribIDFor`:
 *
 *   bytes [0:16] = nonce
 *   bytes [16:24] = uint64 big-endian = (seq << 16) | (index & 0xffff)
 */

import { describe, expect, test } from "bun:test";

import { CONTRIB_ID_BYTES, contribIdFrom, makeNonce, validateContribId } from "../src/contrib.js";
import { InvalidArgumentError } from "../src/index.js";

describe("contrib IDs (#895)", () => {
  test("CONTRIB_ID_BYTES is 24", () => {
    expect(CONTRIB_ID_BYTES).toBe(24);
  });

  test("makeNonce returns 16 random bytes", () => {
    const a = makeNonce();
    const b = makeNonce();
    expect(a.length).toBe(16);
    expect(b.length).toBe(16);
    // Two draws colliding is astronomically unlikely; guards a stuck RNG.
    expect([...a].join(",")).not.toBe([...b].join(","));
  });

  test("contribIdFrom copies the nonce into the high 16 bytes", () => {
    const nonce = new Uint8Array(16).fill(0xab);
    const id = contribIdFrom(nonce, 1n, 0);
    expect(id.length).toBe(24);
    expect([...id.slice(0, 16)]).toEqual([...nonce]);
  });

  test("contribIdFrom encodes (seq<<16)|index big-endian in the low 8 bytes", () => {
    const nonce = new Uint8Array(16);
    const seq = 0x0102030405n;
    const index = 0x0607;
    const id = contribIdFrom(nonce, seq, index);
    const low = new DataView(id.buffer, id.byteOffset + 16, 8).getBigUint64(0, false);
    expect(low).toBe((seq << 16n) | BigInt(index));
  });

  test("contribIdFrom masks the index to 16 bits", () => {
    const nonce = new Uint8Array(16);
    const id = contribIdFrom(nonce, 0n, 0x1_0007); // low 16 bits = 0x0007
    const low = new DataView(id.buffer, id.byteOffset + 16, 8).getBigUint64(0, false);
    expect(low).toBe(0x0007n);
  });

  test("distinct indexes under one seq yield distinct ids", () => {
    const nonce = new Uint8Array(16).fill(1);
    const a = contribIdFrom(nonce, 5n, 0);
    const b = contribIdFrom(nonce, 5n, 1);
    expect([...a].join(",")).not.toBe([...b].join(","));
  });

  test("validateContribId accepts exactly 24 bytes and rejects anything else", () => {
    const ok = new Uint8Array(24);
    expect(validateContribId(ok)).toBe(ok);
    expect(() => validateContribId(new Uint8Array(23))).toThrow(InvalidArgumentError);
    expect(() => validateContribId(new Uint8Array(25))).toThrow(InvalidArgumentError);
    expect(() => validateContribId(new Uint8Array(0))).toThrow(InvalidArgumentError);
  });
});
