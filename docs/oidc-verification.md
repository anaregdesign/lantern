# Registered OIDC verification

This internal verifier delivery tracks #1602 and the login transaction
primitives of #1604. It depends on native security state (#1624). Production
listeners remain guarded until the remaining authorization, browser and HA
boundaries are installed.

API Bearer authentication accepts the signed JWT access-token profile in
[RFC 9068](https://www.rfc-editor.org/rfc/rfc9068.html). The bounded unverified
Issuer selector only selects an exact currently registered Issuer; it never
selects a network destination. Verification pins algorithm, Issuer, audience,
subject, expiry and required profile claims. An ID token or opaque token is not
an alternative API profile. Authentication returns identity evidence only;
current account state, Role evaluation and serving authority remain Server
admission responsibilities.

Login uses Authorization Code with PKCE S256 and independent ID-token
validation under [OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html).
State, nonce, cookie, exact Issuer/client/callback and allowed return path belong
to one bounded single-use transaction on the pinned control process. Restart
invalidates unfinished transactions. Optional at_hash is validated against the
co-returned access token; tokens and code-exchange credentials remain private.
Session issuance separately requires recent auth_time and the current Issuer
configuration revision. Groups/email never grant Roles or link accounts.

Discovery and JWKS use a bounded proxy-free HTTPS fetcher with normal TLS
verification, pinned validated DNS destinations and no redirects. Private
destinations require an exact operator-owned origin/range exception. Management
cannot grant a private-network exception. Secret handles resolve only through
an operator-owned exact Issuer/client/token-endpoint binding; discovery and JWKS
never receive the client secret.

Key refresh is coalesced and bounded by trust entries, concurrency and an
unknown-kid refresh floor. Generation and Issuer configuration revision bind
each cache entry. Warm verification performs no IdP fetch. Expired keys fail
closed on refresh failure. Accepted algorithms use pinned key parameters;
PS256 requires the JOSE hash-length salt without changing global library state.
The JWT library was already a Server dependency; this delivery makes its direct
use explicit and introduces no runtime database or new identity service.

Paired tests exercise HTTPS/DNS/size/redirect boundaries, trust registration,
ambiguous/weak key rejection, rotation/expiry/cache caps, access versus ID-token
profiles, nonce/PKCE/replay/mix-up and exact secret destinations. Initial-key and
warm-key benchmarks include allocations; initial keys use a deterministic local
HTTPS provider and are diagnostic, not a real-provider latency qualification.
Real Connect admission, browser flow and final provider/HA acceptance are
subsequent deliveries.
