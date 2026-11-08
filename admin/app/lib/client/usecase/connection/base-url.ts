/** Fresh production browsers use the same-origin container gateway. */
export function defaultBaseUrl(origin?: string, development = false): string {
  // Vite development has no Server proxy. Saved/direct choices are read first
  // by ConnectionProvider and are never replaced with this initial default.
  const normalized = origin ? normaliseBaseUrl(origin) : null;
  return !development && normalized ? normalized : "http://localhost:6380";
}

export const DEFAULT_BASE_URL = defaultBaseUrl(
  typeof window === "undefined" ? undefined : window.location.origin,
  import.meta.env.DEV,
);

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
  if (url.username || url.password || url.search || url.hash) return null;
  const stripped = `${url.protocol}//${url.host}${url.pathname.replace(/\/$/, "")}`;
  return stripped;
}
