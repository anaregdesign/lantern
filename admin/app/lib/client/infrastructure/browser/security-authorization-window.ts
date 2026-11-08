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
    navigate(value, attemptAffinity) {
      const target = new URL(value);
      // Server binds this one canonical query to member/process/attempt. A
      // generic search allowlist would admit unrelated login parameters.
      const affinity =
        /^v2\.([1-9][0-9]{0,9})\.[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]\.[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.exec(
          attemptAffinity,
        );
      if (
        !affinity ||
        Number(affinity[1]) > 0xffffffff ||
        target.origin !== origin ||
        !/^\/auth\/management-authorization\/[A-Za-z0-9_-]{43}$/.test(
          target.pathname,
        ) ||
        value !== `${origin}${target.pathname}?affinity=${attemptAffinity}` ||
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
