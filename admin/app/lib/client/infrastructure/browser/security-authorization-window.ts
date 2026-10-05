import type { SecurityAuthorizationWindow } from "~/lib/client/usecase/security/security-management";

export function openSecurityAuthorizationWindow(
  baseUrl: string,
): SecurityAuthorizationWindow {
  const origin = new URL(baseUrl).origin;
  if (
    origin !== window.location.origin ||
    new URL(baseUrl).protocol !== "https:"
  )
    throw new Error(
      "Operation authentication requires the trusted Admin origin.",
    );
  const popup = window.open("about:blank", "_blank", "popup");
  if (!popup)
    throw new Error("Allow the reauthentication window, then try again.");
  return {
    navigate(value) {
      const target = new URL(value);
      if (
        target.origin !== origin ||
        !/^\/auth\/management-authorization\/[A-Za-z0-9_-]{43}$/.test(
          target.pathname,
        ) ||
        target.search ||
        target.hash ||
        target.username ||
        target.password
      ) {
        popup.close();
        throw new Error("Invalid operation authentication destination.");
      }
      popup.opener = null;
      popup.location.href = target.href;
    },
    close() {
      popup.close();
    },
  };
}
