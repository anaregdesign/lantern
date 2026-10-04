# Registered OIDC verification

The verifier tracks #1602 and the login transaction primitives of #1604.
The certified production public listener installs authentication, current native
Role admission, browser sessions and bounded authorization leases. Private
policy/replication uses a separate workload listener. The complete boundary and
configuration contract is [ADR 0012](decisions/0012-oidc-prefix-rbac.md).
Source implementation and local conformance do not establish final real-provider,
qualified production clock, device or publication acceptance (#1610).

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
Ordinary session issuance accepts missing `auth_time` as unknown and preserves
older signed evidence; current Issuer configuration, Principal and Role checks
still apply. Future or contradictory signed evidence is rejected. Ordinary
login and replacement do not infer authentication time from `iat`, callback,
consent or account selection, and do not request forced provider reauthentication.
The Server-owned transaction saves an explicit step-up purpose independent of
session replacement. Only step-up requests `max_age=0`, `prompt=login` and an
essential signed `auth_time` through the OIDC `claims` parameter. Step-up and
important security changes require a signed event within five minutes; failure
preserves the existing session. Groups/email never grant Roles or link accounts.
Provider prerequisites and Google's step-up limitation are documented in the
[Google setup runbook](google-oidc-setup.md).

Unknown authentication evidence survives canonical images, signed replication,
checkpoints and restart. The current security cohort is image version 2,
`LNSEC03` revisions and native journal binding v2. Old cohorts are incompatible
and must be rejected before admission or advancing durable floors; see the
[operations upgrade boundary](oidc-operations.md#security-state-version-boundary).

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
Real Connect tests cover certified admission, browser login/session/logout,
management and scope-bound query/CDC/receipt behavior. Actual production startup
and two-process HA local conformance retain the real restart fencing interval.
Final real-provider and deployment clock qualification remain separate #1610 gates.
