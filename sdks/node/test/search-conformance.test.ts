import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import {
  FailedPreconditionError,
  InvalidArgumentError,
  ResourceExhaustedError,
  SearchErrorReason,
  connect,
  type Lantern,
  type SearchOptions,
} from "../src/index.js";

interface ManifestOptions {
  limit?: number;
  prefix?: string;
  match_mode?: "any" | "all" | "min-should";
  min_should_match?: number;
  phrase?: boolean;
  fuzziness?: number;
  prefix_terms?: boolean;
}

interface ManifestCase {
  name: string;
  query: string;
  options: ManifestOptions;
  want_keys?: string[];
  environment?: string;
  reason?: string;
}

interface Manifest {
  version: string;
  vertices: { key: string; value: string }[];
  queries: ManifestCase[];
  invalid: ManifestCase[];
  cancellation: ManifestCase;
  typed_errors: ManifestCase[];
}

const manifest = JSON.parse(
  readFileSync(new URL("../../../testdata/search/conformance.json", import.meta.url), "utf8"),
) as Manifest;

const endpoints = {
  enabled: process.env.LANTERN_NODE_REAL_WIRE_ENDPOINT,
  disabled: process.env.LANTERN_NODE_SEARCH_DISABLED_ENDPOINT,
  "positions-disabled": process.env.LANTERN_NODE_SEARCH_POSITIONS_DISABLED_ENDPOINT,
  "query-budget": process.env.LANTERN_NODE_SEARCH_BUDGET_ENDPOINT,
};

function options(input: ManifestOptions): SearchOptions {
  return {
    limit: input.limit,
    prefix: input.prefix,
    matchMode: input.match_mode,
    minShouldMatch: input.min_should_match,
    phrase: input.phrase,
    fuzziness: input.fuzziness,
    prefixTerms: input.prefix_terms,
  };
}

if (Object.values(endpoints).some((endpoint) => !endpoint)) {
  test("shared search conformance requires all real-wire endpoints", () => {
    expect(manifest.version).toBe("search-conformance-v1");
  });
} else {
  describe("shared production search conformance", () => {
    let client: Lantern;

    beforeAll(async () => {
      client = connect(endpoints.enabled!);
      await client.putVertices(manifest.vertices);
    });

    afterAll(() => client.close());

    for (const fixture of manifest.queries) {
      test(fixture.name, async () => {
        const hits = await client.searchVertices(fixture.query, options(fixture.options));
        expect(hits.map((hit) => hit.key)).toEqual(fixture.want_keys ?? []);
        expect(hits.every((hit) => Number.isFinite(hit.score))).toBe(true);
      });
    }

    for (const fixture of manifest.invalid) {
      test(`invalid/${fixture.name}`, async () => {
        await expect(
          client.searchVertices(fixture.query, options(fixture.options)),
        ).rejects.toBeInstanceOf(InvalidArgumentError);
      });
    }

    test(`cancellation/${manifest.cancellation.name}`, async () => {
      const controller = new AbortController();
      controller.abort();
      await expect(
        client.searchVertices(
          manifest.cancellation.query,
          options(manifest.cancellation.options),
          controller.signal,
        ),
      ).rejects.toBeDefined();
    });

    for (const fixture of manifest.typed_errors) {
      test(`typed/${fixture.name}`, async () => {
        const errorClient = connect(endpoints[fixture.environment as keyof typeof endpoints]!);
        try {
          await errorClient.searchVertices(fixture.query, options(fixture.options));
          expect.unreachable("search should reject");
        } catch (error) {
          switch (fixture.reason) {
            case "search-disabled":
              expect(error).toBeInstanceOf(FailedPreconditionError);
              expect((error as FailedPreconditionError).reason).toBe(
                SearchErrorReason.SEARCH_DISABLED,
              );
              break;
            case "positions-disabled":
              expect(error).toBeInstanceOf(FailedPreconditionError);
              expect((error as FailedPreconditionError).reason).toBe(
                SearchErrorReason.SEARCH_POSITIONS_DISABLED,
              );
              break;
            case "query_bytes":
              expect(error).toBeInstanceOf(ResourceExhaustedError);
              expect((error as ResourceExhaustedError).reason).toBe(
                SearchErrorReason.SEARCH_WORK_BUDGET_EXHAUSTED,
              );
              expect((error as ResourceExhaustedError).workKind).toBe("query_bytes");
              break;
          }
        } finally {
          errorClient.close();
        }
      });
    }
  });
}
