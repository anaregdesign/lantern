import { expect, test, setSystemTime } from "bun:test";
import {
  copySecurityContract,
  currentOriginalBinding,
  CurrentSecurityProgress,
  CurrentSecurityDisposition,
  ManagedLogLevel,
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
  logPolicyRecoveryOwner,
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
function logReview(nonce = 30) {
  const r = review(nonce);
  r.changes = [
    {
      $typeName: "graph.v1.SecurityChange",
      operation: {
        case: "putLogPolicy",
        value: {
          $typeName: "graph.v1.ManagedLogPolicy",
          version: 1,
          level: ManagedLogLevel.WARN,
        },
      },
    },
  ];
  return r;
}
function logOwner(r = logReview(), gateway = "https://admin.example") {
  return logPolicyRecoveryOwner(gateway, {
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

test("the only extra recovery lane is one canonical log policy operation", () => {
  const logging = logReview(),
    backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  expect(JSON.parse(logOwner(logging))).toEqual([
    ...JSON.parse(owner(logging)),
    "log-policy",
  ]);
  store.stage(logOwner(logging), logging);
  expect(store.read(logOwner(logging))?.review).toEqual(logging);
  for (const suffix of ["settings", "security", "log-policy-extra"]) {
    const forged = JSON.stringify([...JSON.parse(owner(logging)), suffix]);
    expect(() =>
      new SecurityChangeRecovery(storage().port).stage(forged, logging),
    ).toThrow();
  }
  const mixed = copySecurityContract(logging);
  mixed.changes.push(review().changes[0]);
  for (const r of [review(40), mixed]) {
    expect(() =>
      new SecurityChangeRecovery(storage().port).stage(logOwner(logging), r),
    ).toThrow();
  }
  for (const r of [logging, mixed]) {
    expect(() =>
      new SecurityChangeRecovery(storage().port).stage(owner(r), r),
    ).toThrow("own recovery lane");
  }
  for (const field of ["version", "level"] as const) {
    const invalid = copySecurityContract(logging);
    const operation = invalid.changes[0].operation;
    if (operation.case !== "putLogPolicy")
      throw new Error("Expected log policy fixture.");
    if (field === "version") operation.value.version = 2;
    else operation.value.level = ManagedLogLevel.UNSPECIFIED;
    expect(() =>
      new SecurityChangeRecovery(storage().port).stage(
        logOwner(invalid),
        invalid,
      ),
    ).toThrow();
  }
});

test("logging ambiguity cannot consume the independent security recovery lane", () => {
  const logging = logReview(41),
    security = review(42),
    backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  store.stage(logOwner(logging), logging);
  store.ambiguous(logging);
  store.stage(owner(security), security);
  const records = JSON.parse(backend.raw()!).records;
  expect(records).toHaveLength(2);
  expect(store.read(logOwner(logging))?.review).toEqual(logging);
  expect(store.read(owner(security))?.review).toEqual(security);
  expect(() => store.stage(logOwner(logging), logReview(43), logging)).toThrow(
    "retained original status",
  );
  store.update(owner(security), security, result(security));
  const rejected = result(
    logging,
    CurrentSecurityProgress.APPLIED,
    CurrentSecurityDisposition.REJECTED_CAS,
  );
  rejected.original!.items[0].kind = "log_policy.put";
  store.update(logOwner(logging), logging, rejected);
  expect(store.read(logOwner(logging))?.needsOriginalStatus).toBe(false);
  expect(currentOriginalBinding(store.read(logOwner(logging))!.review)).toBe(
    currentOriginalBinding(logging),
  );
  expect(store.read(owner(security))?.result?.original).toBeDefined();
});

test("unresolved security history blocks new logging dispatch without overwriting either lane", () => {
  const security = review(44),
    logging = logReview(45),
    backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  store.stage(owner(security), security);
  const before = backend.raw();
  expect(() => store.stage(logOwner(logging), logging)).toThrow(
    "retained security original status",
  );
  expect(backend.raw()).toBe(before);
  expect(store.read(logOwner(logging))).toBeUndefined();
  store.update(owner(security), security, result(security));
  store.stage(logOwner(logging), logging);
  expect(JSON.parse(backend.raw()!).records).toHaveLength(2);
});

test("old four-field log-only indexing is upgraded without changing an unknown original", () => {
  const logging = logReview(46),
    originalRaw = envelope(logging),
    backend = storage(originalRaw);
  const store = new SecurityChangeRecovery(backend.port);
  expect(backend.raw()).toBe(originalRaw);
  expect(store.read(owner(logging))).toBeUndefined();
  expect(store.read(logOwner(logging))?.review).toEqual(logging);
  expect(store.read(logOwner(logging))?.needsOriginalStatus).toBe(true);
  const security = review(47);
  store.stage(owner(security), security);
  const records = JSON.parse(backend.raw()!).records;
  expect(records).toHaveLength(2);
  expect(
    records.find((r: { owner: string }) => r.owner === logOwner(logging))
      .review,
  ).toBe(encodeCurrentSecurityReview(logging));
  const restored = new SecurityChangeRecovery(backend.port);
  expect(currentOriginalBinding(restored.read(logOwner(logging))!.review)).toBe(
    currentOriginalBinding(logging),
  );
  expect(restored.read(owner(security))?.review).toEqual(security);
  expect(restored.read(logOwner(logging))?.needsOriginalStatus).toBe(true);
});

test("lane normalization collisions and persistence failure preserve original raw bytes", () => {
  const logging = logReview(48),
    other = logReview(49);
  const rows = JSON.parse(envelope(logging)).records;
  rows.push({
    owner: logOwner(other),
    review: encodeCurrentSecurityReview(other),
  });
  const conflictingRaw = JSON.stringify({ version: 1, records: rows });
  const conflicting = storage(conflictingRaw),
    rejected = new SecurityChangeRecovery(conflicting.port);
  expect(() => rejected.read(logOwner(logging))).toThrow();
  expect(() => rejected.stage(owner(review(50)), review(50))).toThrow();
  expect(conflicting.raw()).toBe(conflictingRaw);

  const originalRaw = envelope(logging),
    backend = storage(originalRaw),
    store = new SecurityChangeRecovery(backend.port);
  backend.fail();
  expect(() => store.stage(owner(review(51)), review(51))).toThrow();
  expect(backend.raw()).toBe(originalRaw);
  expect(store.read(logOwner(logging))?.review).toEqual(logging);
  expect(store.read(owner(review(51)))).toBeUndefined();
});

test("both bounded lanes still count against the one shared history limit", () => {
  const backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  for (let i = 0; i < 10; i++) {
    const gateway = `https://admin.example/deployment-${i}`;
    const security = review(60 + i * 2),
      logging = logReview(61 + i * 2);
    store.stage(owner(security, gateway), security);
    store.update(owner(security, gateway), security, result(security));
    store.stage(logOwner(logging, gateway), logging);
  }
  const before = backend.raw();
  expect(JSON.parse(before!).records).toHaveLength(20);
  expect(() =>
    store.stage(owner(review(90), "https://admin.example/another"), review(90)),
  ).toThrow("history is full");
  expect(backend.raw()).toBe(before);
});

test("logging cannot occupy the final row needed by its matching security lane", () => {
  const backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  for (let i = 0; i < 19; i++) {
    const security = review(100 + i);
    store.stage(owner(security, `https://admin.example/other-${i}`), security);
  }
  const logging = logReview(120),
    gateway = "https://admin.example/logging";
  const before = backend.raw();
  expect(() => store.stage(logOwner(logging, gateway), logging)).toThrow(
    "history is full",
  );
  expect(backend.raw()).toBe(before);
  expect(store.read(logOwner(logging, gateway))).toBeUndefined();
  // Without the unadmitted logging row, the ordinary final security slot is
  // still available; no prior original was discarded to create it.
  store.stage(owner(review(121), gateway), review(121));
  expect(JSON.parse(backend.raw()!).records).toHaveLength(20);
});

test("the reserved row belongs to the matching actor/deployment and cannot be consumed by another owner", () => {
  const backend = storage(),
    store = new SecurityChangeRecovery(backend.port);
  for (let i = 0; i < 18; i++) {
    const security = review(130 + i);
    store.stage(owner(security, `https://admin.example/other-${i}`), security);
  }
  const logging = logReview(150),
    security = review(151),
    gateway = "https://admin.example/logging";
  store.stage(logOwner(logging, gateway), logging);
  const before = backend.raw();
  expect(JSON.parse(before!).records).toHaveLength(19);
  expect(() =>
    store.stage(
      owner(review(152), "https://admin.example/unrelated"),
      review(152),
    ),
  ).toThrow("history is full");
  expect(backend.raw()).toBe(before);
  store.stage(owner(security, gateway), security);
  expect(JSON.parse(backend.raw()!).records).toHaveLength(20);
  expect(store.read(logOwner(logging, gateway))?.review).toEqual(logging);
  expect(store.read(owner(security, gateway))?.review).toEqual(security);
});

test("a fully retained legacy store remains readable and status-updatable without claiming new capacity", () => {
  const records = Array.from({ length: 20 }, (_, i) => {
    const logging = logReview(160 + i);
    return {
      owner: logOwner(logging, `https://admin.example/legacy-${i}`),
      review: encodeCurrentSecurityReview(logging),
    };
  });
  const backend = storage(JSON.stringify({ version: 1, records })),
    store = new SecurityChangeRecovery(backend.port);
  const logging = logReview(160),
    gateway = "https://admin.example/legacy-0";
  expect(store.read(logOwner(logging, gateway))?.review).toEqual(logging);
  store.update(
    logOwner(logging, gateway),
    logging,
    result(logging, CurrentSecurityProgress.UNRESOLVED),
  );
  expect(JSON.parse(backend.raw()!).records).toHaveLength(20);
  const before = backend.raw();
  expect(() => store.stage(owner(review(181), gateway), review(181))).toThrow(
    "history is full",
  );
  expect(backend.raw()).toBe(before);
  expect(store.read(logOwner(logging, gateway))?.review).toEqual(logging);
});

test("replacing a confirmed legacy logging original cannot bypass its missing security reservation", () => {
  const records = Array.from({ length: 20 }, (_, i) => {
    const logging = logReview(190 + i);
    return {
      owner: logOwner(logging, `https://admin.example/legacy-${i}`),
      review: encodeCurrentSecurityReview(logging),
    };
  });
  const backend = storage(JSON.stringify({ version: 1, records }));
  const store = new SecurityChangeRecovery(backend.port);
  const gateway = "https://admin.example/legacy-0";
  const original = logReview(190);
  const fresh = result(original);
  fresh.original!.items[0].kind = "log_policy.put";
  store.update(logOwner(original, gateway), original, fresh);
  expect(store.read(logOwner(original, gateway))?.needsOriginalStatus).toBe(
    false,
  );
  const before = backend.raw();
  const replacement = logReview(211);
  expect(() =>
    store.stage(logOwner(replacement, gateway), replacement, original),
  ).toThrow("history is full");
  expect(backend.raw()).toBe(before);
  expect(store.read(logOwner(original, gateway))?.review).toEqual(original);
  expect(store.read(logOwner(original, gateway))?.needsOriginalStatus).toBe(
    false,
  );
  // An authenticated status-only update remains possible; it does not admit
  // a new original or pretend the over-budget history has spare capacity.
  store.update(logOwner(original, gateway), original, fresh);
  expect(store.read(logOwner(original, gateway))?.review).toEqual(original);
  expect(JSON.parse(backend.raw()!).records).toHaveLength(20);
});

test("a reserved full store still permits a new security original in its existing matching lane", () => {
  const backend = storage();
  const store = new SecurityChangeRecovery(backend.port);
  const gateway = "https://admin.example/urgent";
  const previous = review(212);
  const logging = logReview(213);
  store.stage(owner(previous, gateway), previous);
  store.update(owner(previous, gateway), previous, result(previous));
  store.stage(logOwner(logging, gateway), logging);
  for (let i = 0; i < 18; i++) {
    const security = review(214 + i);
    store.stage(owner(security, `https://admin.example/other-${i}`), security);
  }
  expect(JSON.parse(backend.raw()!).records).toHaveLength(20);
  const urgent = review(233);
  store.stage(owner(urgent, gateway), urgent, previous);
  expect(store.read(owner(urgent, gateway))?.review).toEqual(urgent);
  expect(store.read(logOwner(logging, gateway))?.review).toEqual(logging);
  expect(store.read(logOwner(logging, gateway))?.needsOriginalStatus).toBe(
    true,
  );
});
