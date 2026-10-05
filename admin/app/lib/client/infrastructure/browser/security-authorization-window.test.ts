import { expect, test } from "bun:test";
import { openSecurityAuthorizationWindow } from "./security-authorization-window";

test("operation authentication opens synchronously and accepts only the exact trusted ticket route", () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, "window");
  let opened = 0,
    closed = 0;
  const popup = {
    opener: {},
    location: { href: "about:blank" },
    close() {
      closed++;
    },
  };
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      location: { origin: "https://admin.example" },
      open(url: string) {
        expect(url).toBe("about:blank");
        opened++;
        return popup;
      },
    },
  });
  try {
    expect(() =>
      openSecurityAuthorizationWindow("http://admin.example"),
    ).toThrow();
    expect(() =>
      openSecurityAuthorizationWindow("https://other.example"),
    ).toThrow();
    const approval = openSecurityAuthorizationWindow("https://admin.example");
    expect(opened).toBe(1);
    const route = "/auth/management-authorization/" + "A".repeat(43);
    approval.navigate("https://admin.example" + route);
    expect(popup.opener).toBeNull();
    expect(popup.location.href).toBe("https://admin.example" + route);
    for (const invalid of [
      "https://other.example" + route,
      "https://admin.example/auth/login",
      "https://admin.example" + route + "?purpose=login",
      "https://admin.example" + route + "#override",
      "https://user@admin.example" + route,
    ]) {
      expect(() => approval.navigate(invalid)).toThrow();
    }
    expect(closed).toBe(5);
    approval.close();
    expect(closed).toBe(6);
  } finally {
    if (previous) Object.defineProperty(globalThis, "window", previous);
    else Reflect.deleteProperty(globalThis, "window");
  }
});
