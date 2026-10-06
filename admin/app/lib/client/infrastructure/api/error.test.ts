import { describe, expect, it } from "bun:test";
import {
  FailedPreconditionError,
  InvalidArgumentError,
  LanternError,
  SearchContinuationLimitedError,
  SearchCursorStaleError,
  SearchErrorReason,
} from "lantern-sdk/web";

import { LanternApiError } from "./error";

describe("LanternApiError search reasons", () => {
  it("classifies only SEARCH_DISABLED as the calm disabled state", () => {
    const disabled = LanternApiError.fromUnknown(
      "SearchVertices",
      new FailedPreconditionError("search disabled", {
        reason: SearchErrorReason.SEARCH_DISABLED,
      }),
    );
    const positions = LanternApiError.fromUnknown(
      "SearchVertices",
      new FailedPreconditionError("positions disabled", {
        reason: SearchErrorReason.SEARCH_POSITIONS_DISABLED,
      }),
    );

    expect(LanternApiError.isDisabled(disabled)).toBe(true);
    expect(LanternApiError.isDisabled(positions)).toBe(false);
    expect((positions as LanternApiError).searchReason).toBe(
      SearchErrorReason.SEARCH_POSITIONS_DISABLED,
    );
  });

  it("does not guess from an untyped FAILED_PRECONDITION", () => {
    const generic = new FailedPreconditionError("another precondition");
    expect(LanternApiError.isDisabled(generic)).toBe(false);
  });

  it("preserves invalid-cursor, stale-cursor, and continuation reasons", () => {
    const cases = [
      {
        input: new InvalidArgumentError("invalid cursor", {
          reason: SearchErrorReason.SEARCH_CURSOR_INVALID,
        }),
        code: "invalid_argument",
        reason: SearchErrorReason.SEARCH_CURSOR_INVALID,
      },
      {
        input: new SearchCursorStaleError("stale cursor"),
        code: "aborted",
        reason: SearchErrorReason.SEARCH_CURSOR_STALE,
      },
      {
        input: new SearchContinuationLimitedError(),
        code: "resource_exhausted",
        reason: SearchErrorReason.SEARCH_CONTINUATION_LIMITED,
      },
    ];
    for (const testCase of cases) {
      const error = LanternApiError.fromUnknown(
        "SearchVertices",
        testCase.input,
      ) as LanternApiError;
      expect(error.code).toBe(testCase.code);
      expect(error.searchReason).toBe(testCase.reason);
    }
  });
});

describe("LanternApiError structured Connect causes", () => {
  it("retains generic SDK statuses without matching message text", () => {
    for (const [code, expected] of [
      [7, "permission_denied"],
      [14, "unavailable"],
      [16, "unauthenticated"],
      [4, "deadline_exceeded"],
      [13, "internal"],
    ] as const) {
      const error = LanternApiError.fromUnknown(
        "CountVerticesByPrefix",
        new LanternError("opaque diagnostic", { cause: { code } }),
      ) as LanternApiError;
      expect(error.code).toBe(expected);
      expect(error.grpcMessage).toBe("opaque diagnostic");
    }
    expect(
      (
        LanternApiError.fromUnknown(
          "ScanVertices",
          new LanternError("permission_denied unavailable"),
        ) as LanternApiError
      ).code,
    ).toBe("unknown");
  });

  it("follows nested causes but stops at cycles and bounded depth", () => {
    const wrapped = new LanternError("wrapped", {
      cause: new Error("intermediate", { cause: { code: 7 } }),
    });
    expect(
      (LanternApiError.fromUnknown("ScanVertices", wrapped) as LanternApiError)
        .code,
    ).toBe("permission_denied");
    const cycle: { cause?: unknown } = {};
    cycle.cause = cycle;
    let deep: unknown = { code: 7 };
    for (let i = 0; i < 8; i++) deep = { cause: deep };
    for (const cause of [
      cycle,
      deep,
      { code: "7" },
      { code: 0 },
      { code: 17 },
      { code: 7.5 },
      null,
    ]) {
      expect(
        (
          LanternApiError.fromUnknown(
            "ScanVertices",
            new LanternError("wrapped", { cause }),
          ) as LanternApiError
        ).code,
      ).toBe("unknown");
    }
  });

  it("keeps native abort and network errors unchanged and handles opaque causes", () => {
    for (const error of [
      new DOMException("canceled", "AbortError"),
      new TypeError("network"),
    ]) {
      expect(LanternApiError.fromUnknown("ScanVertices", error)).toBe(error);
    }
    const cause = Object.defineProperty({}, "code", {
      get() {
        throw new Error("opaque");
      },
    });
    expect(
      (
        LanternApiError.fromUnknown(
          "ScanVertices",
          new LanternError("wrapped", { cause }),
        ) as LanternApiError
      ).code,
    ).toBe("unknown");
  });
});
