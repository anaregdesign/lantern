/**
 * Default Lantern gateway base URL. Matches the gateway's default listener
 * (`:6381`) documented in `server/README.md`.
 */
export const DEFAULT_BASE_URL = "http://localhost:6381";

/**
 * Normalises a user-entered base URL by trimming whitespace and stripping a
 * trailing slash. Returns `null` for inputs that cannot be parsed as a valid
 * absolute URL with an http(s) scheme.
 */
export function normaliseBaseUrl(input: string): string | null {
  const trimmed = input.trim();
  if (trimmed === "") {
    return null;
  }
  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return null;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return null;
  }
  const stripped = `${url.protocol}//${url.host}${url.pathname.replace(/\/$/, "")}`;
  return stripped === `${url.protocol}//${url.host}` ? stripped : stripped;
}
