import { expect, test, setSystemTime } from "bun:test";
import {
  copySecurityContract,
  currentOriginalBinding,
  CurrentSecurityProgress,
  encodeCurrentSecurityReview,
  encodeCurrentSecurityChangeResult,
  type CurrentSecurityReview,
} from "lantern-sdk/web";
import { buildIssuer, issuerDraft } from "./security-drafts";
import {
  review,
  result,
  version,
  bytes,
} from "../../../../../test/current-security";
import {
  SecurityChangeRecovery,
  securityRecoveryOwner,
  type SecurityRecoveryStorage,
} from "./security-change-recovery";

function storage(initial: string | null = null) {
  let raw = initial,
    fail = false;
  const port: SecurityRecoveryStorage = {
    read: () => raw,
    replace(value) {
      if (fail) throw new Error("quota");
      raw = value;
    },
  };
  return {
    port,
    raw: () => raw,
    fail: (value = true) => {
      fail = value;
    },
  };
}
function owner(r = review(), gateway = "https://admin.example") {
  return securityRecoveryOwner(gateway, {
    identity: r.actor,
    version: { ...version, currentProfile: r.profile },
  });
}
function envelope(r = review(), outcome?: ReturnType<typeof result>) {
  return JSON.stringify({
    version: 1,
    records: [
      {
        owner: owner(r),
        review: encodeCurrentSecurityReview(r),
        ...(outcome
          ? { result: encodeCurrentSecurityChangeResult(outcome) }
          : {}),
      },
    ],
  });
}

test("document replacement preserves complete original and requires fresh status", () => {
  const r = review();
  r.changeId!.namespace = 9007199254740997n;
  r.expectedCut!.sequence = 9007199254740999n;
  const backend = storage(),
    first = new SecurityChangeRecovery(backend.port);
  first.stage(owner(r), r);
  first.ambiguous(r);
  const after = new SecurityChangeRecovery(backend.port);
  expect(after.read(owner(r))?.review).toEqual(r);
  expect(after.read(owner(r))?.needsOriginalStatus).toBe(true);
  expect(() => after.stage(owner(r), review(8), r)).toThrow();
  const retained = after.read(owner(r))!;
  retained.review.intentDigest.fill(9);
  expect(after.read(owner(r))?.review.intentDigest).toEqual(r.intentDigest);
  expect(backend.raw()).not.toContain("authorizationProof");
  expect(backend.raw()).not.toContain("csrfToken");
});

test("cached terminal plus weaker fresh status cannot unlock a new operation", () => {
  const r = review(),
    backend = storage(envelope(r, result(r)));
  const restored = new SecurityChangeRecovery(backend.port);
  expect(restored.read(owner())?.needsOriginalStatus).toBe(true);
  restored.update(owner(), r, result(r, CurrentSecurityProgress.UNRESOLVED));
  expect(restored.read(owner())?.result?.original).toEqual(result(r).original);
  expect(restored.read(owner())?.needsOriginalStatus).toBe(true);
  expect(() => restored.stage(owner(), review(8), r)).toThrow();
  restored.update(owner(), r, result(r));
  expect(restored.read(owner())?.needsOriginalStatus).toBe(false);
  restored.stage(owner(), review(8), r);
  expect(restored.read(owner())?.review).toEqual(review(8));
});

test("retained Issuer intent preserves its operator handle and excludes purpose credentials", () => {
  const r = review() as CurrentSecurityReview & {
    authorizationProof: string;
    csrfToken: string;
  };
  r.changes[0].operation = {
    case: "putIssuer",
    value: buildIssuer({
      ...issuerDraft(),
      issuer: "https://idp.example",
      clientId: "admin",
      apiAudience: "lantern",
      redirectUri: "https://admin.example/auth/callback",
      secretMode: "replace",
      secretRef: "synthetic-operator-binding",
    }),
  };
  r.authorizationProof = "synthetic-purpose-proof";
  r.csrfToken = "synthetic-csrf";
  const backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  store.stage(owner(r), r);
  const restored = new SecurityChangeRecovery(backend.port).read(owner(r))!;
  expect(restored.review.changes).toEqual(r.changes);
  expect(backend.raw()).toContain("synthetic-operator-binding");
  expect(backend.raw()).not.toContain("synthetic-purpose-proof");
  expect(backend.raw()).not.toContain("synthetic-csrf");
});

test("gateway path/port, issuer, subject and every full profile component isolate history", () => {
  const r = review(),
    backend = storage(envelope(r));
  const store = new SecurityChangeRecovery(backend.port);
  for (const gateway of [
    "https://admin.example:54473",
    "https://admin.example/api",
  ]) {
    expect(store.read(owner(r, gateway))).toBeUndefined();
  }
  for (const field of ["issuer", "subject"] as const) {
    const other = copySecurityContract(r);
    other.actor![field] += "-other";
    expect(store.read(owner(other))).toBeUndefined();
  }
  for (const field of [
    "domain",
    "cohort",
    "generation",
    "protocol",
    "timeProfile",
    "membership",
    "configuration",
  ] as const) {
    const other = copySecurityContract(r);
    other.profile![field][0] ^= 1;
    expect(store.read(owner(other))).toBeUndefined();
  }
  expect(() => owner(r, "https://user:password@admin.example")).toThrow();
  expect(() => owner(r, "https://admin.example?route=other")).toThrow();
  expect(store.read(owner(r))?.review).toEqual(r);
  expect(() =>
    owner({ ...r, profile: { ...r.profile!, version: 3 } }),
  ).toThrow();
  expect(backend.raw()).toBe(envelope(r));
});

test("cut, admission and credential rotation do not rename original owner", () => {
  const r = review();
  const rotated = {
    identity: r.actor,
    version: {
      ...version,
      admissionBinding: bytes(32, 99),
      currentCut: { ...version.currentCut!, sequence: 900n },
    },
  };
  expect(securityRecoveryOwner(" https://admin.example/ ", rotated)).toBe(
    owner(r),
  );
});

test("a normalized gateway with a trailing path delimiter survives decoding", () => {
  const r = review(),
    backend = storage();
  const key = owner(r, "https://admin.example/api//");
  new SecurityChangeRecovery(backend.port).stage(key, r);
  expect(new SecurityChangeRecovery(backend.port).read(key)?.review).toEqual(r);
});

test("write/update/clear failure preserves the last persisted original", () => {
  const r = review(),
    backend = storage();
  const store = new SecurityChangeRecovery(backend.port);
  backend.fail();
  expect(() => store.stage(owner(), r)).toThrow("storage");
  expect(backend.raw()).toBeNull();
  backend.fail(false);
  store.stage(owner(), r);
  const originalBytes = backend.raw();
  backend.fail();
  expect(() => store.update(owner(), r, result(r))).toThrow("storage");
  expect(store.read(owner())?.result).toBeUndefined();
  expect(() => store.clearFirstRefusal(owner(), r)).toThrow("storage");
  expect(backend.raw()).toBe(originalBytes);
  expect(
    new SecurityChangeRecovery(backend.port).read(owner())?.review,
  ).toEqual(r);
  backend.fail(false);
  store.clearFirstRefusal(owner(), r);
  expect(store.read(owner())).toBeUndefined();
});

test("an ambiguous or restored original cannot be removed by a later first-refusal claim", () => {
  const r = review(),
    backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  store.stage(owner(), r);
  store.ambiguous(r);
  expect(() => store.clearFirstRefusal(owner(), r)).toThrow();
  const after = new SecurityChangeRecovery(backend.port);
  expect(() => after.clearFirstRefusal(owner(), r)).toThrow();
  expect(after.read(owner())?.review).toEqual(r);
});

test("late controller cannot update or clear a newly staged original", () => {
  const r = review(),
    next = review(8),
    backend = storage();
  const store = new SecurityChangeRecovery(backend.port);
  store.stage(owner(), r);
  store.update(owner(), r, result(r));
  store.stage(owner(), next, r);
  expect(() => store.update(owner(), r, result(r))).toThrow();
  expect(() => store.clearFirstRefusal(owner(), r)).toThrow();
  expect(() => store.stage(owner(), review(9), r)).toThrow();
  expect(store.read(owner())?.review).toEqual(next);
});

test("20 owners and one MiB reject new staging without evicting old ambiguity", () => {
  const backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  for (let i = 0; i < 20; i++) {
    const r = review(i + 1);
    r.actor!.subject = "owner-" + i;
    store.stage(owner(r), r);
  }
  const prior = backend.raw(),
    extra = review(30);
  extra.actor!.subject = "extra";
  expect(() => store.stage(owner(extra), extra)).toThrow("full");
  expect(backend.raw()).toBe(prior);
  const other = storage(),
    bounded = new SecurityChangeRecovery(other.port);
  const huge = review();
  huge.changes[0].operation = {
    case: "deleteRole",
    value: "あ".repeat(400000),
  };
  expect(() => bounded.stage(owner(), huge)).toThrow("full");
  expect(other.raw()).toBeNull();
});

test("corrupt, unknown, duplicate, mismatched and oversized storage is preserved and blocked", () => {
  const good = JSON.parse(envelope()),
    row = good.records[0];
  const badReview = JSON.parse(row.review);
  const badResult = result();
  badResult.original!.items = [];
  const variants = [
    "{",
    JSON.stringify({ ...good, version: 2 }),
    JSON.stringify({ ...good, authority: true }),
    JSON.stringify({ ...good, records: [row, row] }),
    JSON.stringify({
      ...good,
      records: [
        row,
        { ...row, owner: owner(review(), "https://admin.example/api") },
      ],
    }),
    JSON.stringify({ ...good, records: [{ ...row, owner: "other" }] }),
    JSON.stringify({ ...good, records: [{ ...row, csrf: "must-not-load" }] }),
    JSON.stringify({
      ...good,
      records: [
        {
          ...row,
          review: JSON.stringify({
            ...badReview,
            authorizationProof: "secret",
          }),
        },
      ],
    }),
    JSON.stringify({
      ...good,
      records: [
        {
          ...row,
          review: JSON.stringify({
            ...badReview,
            actor: { ...badReview.actor, subject: "different" },
          }),
        },
      ],
    }),
    JSON.stringify({
      ...good,
      records: [
        { ...row, review: JSON.stringify({ ...badReview, changes: [{}] }) },
      ],
    }),
    JSON.stringify({
      ...good,
      records: [
        { ...row, result: encodeCurrentSecurityChangeResult(badResult) },
      ],
    }),
    " ".repeat((1 << 20) + 1),
  ];
  for (const raw of variants) {
    const backend = storage(raw),
      store = new SecurityChangeRecovery(backend.port);
    expect(() => store.read(owner())).toThrow();
    expect(() => store.stage(owner(), review())).toThrow();
    expect(backend.raw()).toBe(raw);
  }
  const unreadable = new SecurityChangeRecovery({
    read: () => {
      throw new Error("access");
    },
    replace: () => {
      throw new Error("unexpected overwrite");
    },
  });
  expect(() => unreadable.read(owner())).toThrow();
});

test("elapsed credential/purpose time never expires retained ambiguity", () => {
  const backend = storage(envelope());
  try {
    setSystemTime(new Date("2100-01-01T00:00:00Z"));
    const after = new SecurityChangeRecovery(backend.port);
    expect(after.read(owner())?.review).toEqual(review());
    expect(after.read(owner())?.needsOriginalStatus).toBe(true);
    expect(currentOriginalBinding(after.read(owner())!.review)).toBe(
      currentOriginalBinding(review()),
    );
    // The envelope has no clock/expiry/approval fields and performs no RPC.
    expect(Object.keys(JSON.parse(backend.raw()!))).toEqual([
      "version",
      "records",
    ]);
  } finally {
    setSystemTime();
  }
});
