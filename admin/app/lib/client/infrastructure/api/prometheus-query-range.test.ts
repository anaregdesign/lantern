import { afterEach, describe, expect, it } from "bun:test";

import {
  PrometheusError,
  parseSampleValue,
  queryRange,
} from "./prometheus-query-range";

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
});

type FetchArgs = { url: string; init?: RequestInit };

/** Replaces global fetch with a stub returning `response`, capturing args. */
function stubFetch(response: Response | (() => never)): FetchArgs {
  const captured: FetchArgs = { url: "" };
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    captured.url = String(input);
    captured.init = init;
    if (typeof response === "function") {
      response();
    }
    return Promise.resolve(response as Response);
  }) as typeof fetch;
  return captured;
}

function matrix(
  result: Array<{
    metric: Record<string, string>;
    values: Array<[number, string]>;
  }>,
): Response {
  return new Response(
    JSON.stringify({
      status: "success",
      data: { resultType: "matrix", result },
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

describe("queryRange", () => {
  it("builds the query_range URL with start/end/step", async () => {
    const captured = stubFetch(matrix([]));
    await queryRange("/api/prom", {
      query: "up",
      start: 1000.7,
      end: 2000.2,
      step: 30,
    });
    expect(captured.url).toBe(
      "/api/prom/api/v1/query_range?query=up&start=1000&end=2000&step=30s",
    );
  });

  it("url-encodes the PromQL expression", async () => {
    const captured = stubFetch(matrix([]));
    await queryRange("http://localhost:9090", {
      query: "sum by (grpc_method) (rate(grpc_server_handled_total[1m]))",
      start: 0,
      end: 60,
      step: 15,
    });
    expect(captured.url).toContain("query=sum+by+%28grpc_method%29");
    expect(
      captured.url.startsWith("http://localhost:9090/api/v1/query_range?"),
    ).toBe(true);
  });

  it("maps a matrix response into MetricSeries with parsed points", async () => {
    stubFetch(
      matrix([
        {
          metric: { __name__: "lantern_vertices", instance: "a" },
          values: [
            [1000, "12"],
            [1030, "13.5"],
          ],
        },
      ]),
    );
    const series = await queryRange("/api/prom", {
      query: "lantern_vertices",
      start: 1000,
      end: 1030,
      step: 30,
    });
    expect(series).toHaveLength(1);
    expect(series[0].labels).toEqual({
      __name__: "lantern_vertices",
      instance: "a",
    });
    expect(series[0].points).toEqual([
      { t: 1000, v: 12 },
      { t: 1030, v: 13.5 },
    ]);
  });

  it("returns multiple series for a `by`-grouped query", async () => {
    stubFetch(
      matrix([
        { metric: { grpc_method: "GetVertex" }, values: [[0, "1"]] },
        { metric: { grpc_method: "PutVertex" }, values: [[0, "2"]] },
      ]),
    );
    const series = await queryRange("/api/prom", {
      query: "x",
      start: 0,
      end: 0,
      step: 1,
    });
    expect(series.map((s) => s.labels.grpc_method)).toEqual([
      "GetVertex",
      "PutVertex",
    ]);
  });

  it("parses special float sample values", async () => {
    stubFetch(
      matrix([
        {
          metric: {},
          values: [
            [0, "NaN"],
            [1, "+Inf"],
            [2, "-Inf"],
          ],
        },
      ]),
    );
    const series = await queryRange("/api/prom", {
      query: "x",
      start: 0,
      end: 2,
      step: 1,
    });
    expect(Number.isNaN(series[0].points[0].v)).toBe(true);
    expect(series[0].points[1].v).toBe(Number.POSITIVE_INFINITY);
    expect(series[0].points[2].v).toBe(Number.NEGATIVE_INFINITY);
  });

  it("throws a status PrometheusError when Prometheus replies status:error", async () => {
    stubFetch(
      new Response(
        JSON.stringify({
          status: "error",
          errorType: "bad_data",
          error: "invalid parameter",
        }),
        { status: 400 },
      ),
    );
    await expect(
      queryRange("/api/prom", { query: "(", start: 0, end: 1, step: 1 }),
    ).rejects.toMatchObject({ kind: "status" });
  });

  it("throws an http PrometheusError for a non-JSON error body", async () => {
    stubFetch(new Response("502 Bad Gateway", { status: 502 }));
    const err = await queryRange("/api/prom", {
      query: "x",
      start: 0,
      end: 1,
      step: 1,
    }).catch((e) => e);
    expect(err).toBeInstanceOf(PrometheusError);
    expect((err as PrometheusError).kind).toBe("http");
    expect((err as PrometheusError).httpStatus).toBe(502);
  });

  it("throws a parse PrometheusError for an SPA index.html fallback (HTTP 200 HTML)", async () => {
    stubFetch(
      new Response("<!DOCTYPE html><html><body>app</body></html>", {
        status: 200,
        headers: { "Content-Type": "text/html" },
      }),
    );
    await expect(
      queryRange("/api/prom", { query: "x", start: 0, end: 1, step: 1 }),
    ).rejects.toMatchObject({ kind: "parse" });
  });

  it("throws a parse PrometheusError when resultType is not matrix", async () => {
    stubFetch(
      new Response(
        JSON.stringify({
          status: "success",
          data: { resultType: "vector", result: [] },
        }),
        { status: 200 },
      ),
    );
    await expect(
      queryRange("/api/prom", { query: "x", start: 0, end: 1, step: 1 }),
    ).rejects.toMatchObject({ kind: "parse" });
  });

  it("throws a network PrometheusError when fetch rejects", async () => {
    stubFetch(() => {
      throw new TypeError("Failed to fetch");
    });
    await expect(
      queryRange("/api/prom", { query: "x", start: 0, end: 1, step: 1 }),
    ).rejects.toMatchObject({ kind: "network" });
  });

  it("rethrows AbortError untouched (not wrapped in PrometheusError)", async () => {
    stubFetch(() => {
      const abort = new Error("aborted");
      abort.name = "AbortError";
      throw abort;
    });
    const err = await queryRange("/api/prom", {
      query: "x",
      start: 0,
      end: 1,
      step: 1,
    }).catch((e) => e);
    expect(err).not.toBeInstanceOf(PrometheusError);
    expect((err as Error).name).toBe("AbortError");
  });
});

describe("parseSampleValue", () => {
  it("parses finite numbers", () => {
    expect(parseSampleValue("3.14")).toBe(3.14);
    expect(parseSampleValue("0")).toBe(0);
    expect(parseSampleValue("-7")).toBe(-7);
  });

  it("maps the Prometheus special-float encodings", () => {
    expect(Number.isNaN(parseSampleValue("NaN"))).toBe(true);
    expect(parseSampleValue("+Inf")).toBe(Number.POSITIVE_INFINITY);
    expect(parseSampleValue("-Inf")).toBe(Number.NEGATIVE_INFINITY);
  });
});
