import { expect, test } from "bun:test";
import { browserSecurityRecoveryStorage } from "./security-change-recovery-storage";

test("browser recovery adapter uses only its dedicated sessionStorage key and surfaces access failure", () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, "window");
  const data = new Map<string, string>([["unrelated", "keep"]]);
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      sessionStorage: {
        getItem: (key: string) => data.get(key) ?? null,
        setItem: (key: string, value: string) => {
          data.set(key, value);
        },
      },
    },
  });
  try {
    const adapter = browserSecurityRecoveryStorage();
    expect(adapter.read()).toBeNull();
    adapter.replace("retained");
    expect(adapter.read()).toBe("retained");
    expect(data.get("unrelated")).toBe("keep");
    expect([...data.keys()]).toEqual([
      "unrelated",
      "lantern.admin.security-change-recovery.v1",
    ]);
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: {
        get sessionStorage() {
          throw new Error("blocked");
        },
      },
    });
    expect(() => adapter.read()).toThrow("blocked");
    expect(() => adapter.replace("new")).toThrow("blocked");
  } finally {
    if (previous) Object.defineProperty(globalThis, "window", previous);
    else Reflect.deleteProperty(globalThis, "window");
  }
});
