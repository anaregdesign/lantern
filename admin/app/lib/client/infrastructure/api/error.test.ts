import { describe, expect, it } from "bun:test";
import {
  FailedPreconditionError,
  InvalidArgumentError,
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
