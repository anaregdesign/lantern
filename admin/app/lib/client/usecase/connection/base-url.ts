/**
 * Default Lantern primary listener URL. Matches the production
 * default (`:6380`) documented in `server/README.md`. Since the #347
 * cutover the primary listener multiplexes Connect / gRPC / gRPC-Web
 * on the same h2c socket, so no separate gateway port is required.
 */
export const DEFAULT_BASE_URL = "http://localhost:6380";

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
