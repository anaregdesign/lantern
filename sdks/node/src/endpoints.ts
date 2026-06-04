/**
 * Static multi-endpoint helper for `Lantern.connectEndpoints`.
 *
 * gRPC's built-in `ipv4:` resolver accepts a comma-separated address list
 * and feeds each into the configured load-balancing policy. Combined with
 * the SDK's default `round_robin` service config the channel fans out
 * across all replicas, rotating away from failed sub-channels automatically.
 *
 * Mirrors `sdks/go/endpoints.go` and `sdks/python/.../endpoints.py`.
 */

export function staticTarget(endpoints: readonly string[]): string {
  if (endpoints.length === 0) {
    throw new Error("at least one endpoint is required");
  }
  const cleaned: string[] = [];
  for (const ep of endpoints) {
    const trimmed = ep.trim();
    if (!trimmed) throw new Error("endpoint may not be empty");
    if (!trimmed.includes(":")) {
      throw new Error(`endpoint ${JSON.stringify(trimmed)} must be host:port`);
    }
    cleaned.push(trimmed);
  }
  return "ipv4:" + cleaned.join(",");
}

export function hasEndpoints(target: string): boolean {
  return target.startsWith("ipv4:") || target.startsWith("ipv6:");
}
