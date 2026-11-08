import { expect, test } from "bun:test";
import { OperationalGroup } from "lantern-sdk/web";
import {
  operationalDefinitions,
  operationalDraft,
  operationalValues,
  operationalFieldError,
  operationalDraftErrors,
} from "./operational-settings-fields";
test("operational forms expose precisely forty-three finite settings in eight atomic groups", () => {
  expect(operationalDefinitions).toHaveLength(8);
  expect(
    operationalDefinitions.reduce((sum, g) => sum + g.fields.length, 0),
  ).toBe(43);
  expect(new Set(operationalDefinitions.map((g) => g.group)).size).toBe(8);
  expect(
    operationalDefinitions
      .flatMap((g) => g.fields)
      .some((f) => /secret|listener|storage|custody/i.test(f.key)),
  ).toBe(false);
});
test("operational interval and unsigned lifecycle drafts retain exact integers beyond Number precision", () => {
  const backup = operationalValues(OperationalGroup.BACKUP, {
    enabled: "true",
    intervalNanos: "9007199254740999",
    retain: "0",
    arbitraryKey: "not serialized",
  });
  expect(backup.group.case).toBe("backup");
  if (backup.group.case !== "backup") throw new Error();
  expect(backup.group.value.intervalNanos).toBe(9007199254740999n);
  expect(operationalDraft(backup).intervalNanos).toBe("9007199254740999");
  expect(backup.group.value).not.toHaveProperty("arbitraryKey");
  const lifecycle = operationalValues(OperationalGroup.LIFECYCLE, {
    drainDelaySeconds: "0",
    maxReplicationLag: "18446744073709551615",
    shutdownTimeoutSeconds: "30",
  });
  expect(operationalDraft(lifecycle).maxReplicationLag).toBe(
    "18446744073709551615",
  );
  expect(() =>
    operationalValues(OperationalGroup.BACKUP, {
      enabled: "true",
      intervalNanos: "9.2",
      retain: "0",
    }),
  ).toThrow();
  expect(() =>
    operationalValues(OperationalGroup.BACKUP, {
      enabled: "true",
      intervalNanos: "9223372036854775808",
      retain: "0",
    }),
  ).toThrow();
});
test("operational rate edits preserve fractional RPS and validate the pair together", () => {
  const values = operationalValues(OperationalGroup.RATE, {
    rps: "0.25",
    burst: "1",
  });
  expect(operationalDraft(values)).toEqual({ rps: "0.25", burst: "1" });
  expect(() =>
    operationalValues(OperationalGroup.RATE, { rps: "0", burst: "1" }),
  ).toThrow();
  expect(() =>
    operationalValues(OperationalGroup.RATE, { rps: "NaN", burst: "1" }),
  ).toThrow();
  expect(() =>
    operationalValues(OperationalGroup.SLOW_RPC, { thresholdMs: "-1" }),
  ).toThrow();
});

test("operational rate draft roundtrips tiny and large finite scientific values", () => {
  for (const rps of [1e-100, 1e100, Number.MIN_VALUE, Number.MAX_VALUE, 0.25]) {
    const values = operationalValues(OperationalGroup.RATE, {
      rps: String(rps),
      burst: "1",
    });
    const roundtrip = operationalValues(
      OperationalGroup.RATE,
      operationalDraft(values),
    );
    expect(roundtrip).toEqual(values);
  }
  for (const rps of ["-0", "-1e-100", "1e999", "Infinity"]) {
    expect(() =>
      operationalValues(OperationalGroup.RATE, { rps, burst: "1" }),
    ).toThrow();
  }
});

test("operational Search fields show and enforce the existing capability uint32 maximum", () => {
  const draft = {
    defaultLimit: "100",
    maxLimit: "1000",
    defaultMode: "1",
    defaultMinShould: "1",
    timeoutMs: "4294967295",
    maxQueryBytes: "4294967295",
    maxQueryTerms: "4294967295",
    maxDictionaryVisits: "100",
    maxPostingVisits: "100",
    maxPositionVisits: "100",
    maxExpirationVisits: "100",
  };
  expect(() => operationalValues(OperationalGroup.SEARCH, draft)).not.toThrow();
  const fields = operationalDefinitions.find(
    (d) => d.group === OperationalGroup.SEARCH,
  )!.fields;
  for (const key of ["timeoutMs", "maxQueryBytes", "maxQueryTerms"]) {
    expect(fields.find((f) => f.key === key)?.max).toBe("4294967295");
    expect(() =>
      operationalValues(OperationalGroup.SEARCH, {
        ...draft,
        [key]: "4294967296",
      }),
    ).toThrow("at most 4294967295");
  }
});

test("every operational field has accurate complete input guidance and an SDK-valid example group", () => {
  for (const group of operationalDefinitions) {
    const examples: Record<string, string> = {};
    for (const field of group.fields) {
      expect(field.help.meaning.length).toBeGreaterThan(15);
      expect(field.help.details).toContain("Fresh startup default:");
      expect(field.help.details).toContain("Empty is invalid");
      expect(operationalFieldError(field, field.help.example)).toBeUndefined();
      expect(operationalFieldError(field, "")).toBeDefined();
      examples[field.key] = field.help.example;
    }
    expect(() => operationalValues(group.group, examples)).not.toThrow();
    expect(operationalDraftErrors(group.group, examples)).toEqual({});
  }
});
test("row feedback identifies both fields of invalid policy pairs and exact integer boundaries", () => {
  expect(
    operationalDraftErrors(OperationalGroup.RATE, { rps: "0", burst: "1" }),
  ).toEqual({
    rps: "RPS and Burst must both be zero or both positive.",
    burst: "RPS and Burst must both be zero or both positive.",
  });
  const group = operationalDefinitions.find(
    (g) => g.group === OperationalGroup.REPLICATION,
  )!;
  const draft = Object.fromEntries(
    group.fields.map((f) => [f.key, f.help.example]),
  );
  const errors = operationalDraftErrors(group.group, {
    ...draft,
    pumpBackoffMinMs: "30001",
  });
  expect(Object.keys(errors)).toEqual(["pumpBackoffMinMs", "pumpBackoffMaxMs"]);
  const threshold = operationalDefinitions[0]!.fields[0]!;
  expect(operationalFieldError(threshold, "9223372036854")).toBeUndefined();
  expect(operationalFieldError(threshold, "9223372036855")).toContain(
    "9223372036854",
  );
  expect(() =>
    operationalValues(OperationalGroup.SLOW_RPC, {
      thresholdMs: "9223372036855",
    }),
  ).toThrow();
});
