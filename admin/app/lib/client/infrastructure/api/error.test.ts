import { describe, expect, it } from "bun:test";
import { FailedPreconditionError, SearchErrorReason } from "lantern-sdk/web";

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
});
