import { expect, test } from "bun:test";
import { SecurityAuthorizationRequirement } from "lantern-sdk/web";
import {
  review,
  version,
  changes,
  bytes,
} from "../../../../../test/current-security";
import {
  SecurityDraftRecovery,
  recoverableSecurityReview,
  type SecurityDraftIntent,
} from "./security-draft-recovery";

function intent(): SecurityDraftIntent {
  return {
    binding: "rpc-policy-binding",
    policyLoaded: true,
    review: {
      label: "Unsent change",
      changes,
      version,
      contract: review(),
      approval: "approved",
      requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
      authorizationId: bytes(32, 8),
      attemptAffinity: "existing-attempt",
      authorizationProof: bytes(32, 9),
    },
  };
}
test("draft recovery keeps exact review/attempt metadata but never approval proof", () => {
  const store = new SecurityDraftRecovery();
  const owner = {};
  const source = intent();
  store.write(owner, "issuers", source);
  const value = store.read(owner, "issuers")!;
  expect(value.review?.approval).toBe("authenticating");
  expect(value.review?.authorizationProof).toBeUndefined();
  expect(value.review?.contract).toEqual(source.review?.contract);
  expect(value.review?.authorizationId).toEqual(bytes(32, 8));
  value.review!.label = "mutated returned clone";
  source.review!.label = "mutated input";
  expect(store.read(owner, "issuers")?.review?.label).toBe("Unsent change");
});
test("an interrupted preparation without a contract is not a recoverable operation", () => {
  expect(
    recoverableSecurityReview({
      label: "preparing",
      changes,
      version,
      approval: "preparing",
    }),
  ).toBeUndefined();
});
test("draft metadata has an absolute expiry even when read or written during revalidation", () => {
  let now = 0;
  const store = new SecurityDraftRecovery(() => now);
  const owner = {};
  store.write(owner, "issuers", intent());
  now = 20 * 60_000;
  expect(store.read(owner, "issuers")).toBeDefined();
  store.write(owner, "issuers", intent());
  now = 30 * 60_000;
  expect(store.read(owner, "issuers")).toBeUndefined();
});
test("owner changes, explicit cleanup and the byte cap discard editing metadata", () => {
  const store = new SecurityDraftRecovery();
  const first = {},
    other = {};
  for (const section of ["issuers", "roles", "users"] as const)
    store.write(first, section, intent());
  expect(store.read(other, "issuers")).toBeUndefined();
  expect(store.read(first, "roles")).toBeUndefined();
  store.write(first, "issuers", {
    ...intent(),
    binding: "x".repeat(1024 * 1024 + 1),
  });
  expect(store.read(first, "issuers")).toBeUndefined();
  store.write(first, "users", intent());
  store.clear();
  expect(store.read(first, "users")).toBeUndefined();
});
