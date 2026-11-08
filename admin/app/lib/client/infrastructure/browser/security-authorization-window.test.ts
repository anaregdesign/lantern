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
    const affinity = `v2.1.${"A".repeat(43)}.${"C".repeat(42)}A`;
    const destination = `https://admin.example${route}?affinity=${affinity}`;
    approval.navigate(destination, affinity);
    expect(popup.opener).toBeNull();
    expect(popup.location.href).toBe(destination);
    const invalidDestinations = [
      destination.replace("admin.example", "other.example"),
      `https://admin.example/auth/login?affinity=${affinity}`,
      "https://admin.example" + route,
      "https://admin.example" + route + "?affinity=",
      destination + "&purpose=login",
      destination + `&affinity=${affinity}`,
      destination.replace("?affinity=", "?%61ffinity="),
      destination.replace("v2.1.", "v2%2E1."),
      destination.replace("v2.1.", "v2.2."),
      destination + "#override",
      destination + "#",
      destination.replace("https://", "https://user@"),
    ];
    for (const invalid of invalidDestinations) {
      expect(() => approval.navigate(invalid, affinity)).toThrow();
    }
    const invalidAffinities = [
      "",
      "node-process-attempt",
      affinity.replace("v2.1.", "v2.0."),
      affinity.replace("v2.1.", "v2.01."),
      affinity.replace("v2.1.", "v2.4294967296."),
      affinity + "A",
      affinity.slice(0, -1) + "B",
    ];
    for (const invalid of invalidAffinities) {
      expect(() =>
        approval.navigate(
          `https://admin.example${route}?affinity=${invalid}`,
          invalid,
        ),
      ).toThrow();
    }
    expect(closed).toBe(invalidDestinations.length + invalidAffinities.length);
    approval.close();
    expect(closed).toBe(
      invalidDestinations.length + invalidAffinities.length + 1,
    );
  } finally {
    if (previous) Object.defineProperty(globalThis, "window", previous);
    else Reflect.deleteProperty(globalThis, "window");
  }
});
