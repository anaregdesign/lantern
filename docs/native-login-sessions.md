# Native Google/Apple login — #1750 design checkpoint

Status: selected design, recorded before implementation and consumers on
2026-10-13; the current candidate implements this contract. This document alone
is not implementation or provider/device acceptance evidence.
The authoritative scope is [#1750](https://github.com/anaregdesign/lantern/issues/1750),
including its three comments read at this checkpoint. The inspected main is
`4a4fa11cc3f74affcedc7b9bb6ece029c5cc73ef`; its six applicable push workflows
passed. The prerequisite [#1751](https://github.com/anaregdesign/lantern/issues/1751)
landed through #1752 at `f8db9d460e9ddac5cc9afcdf0870e485a4d2d0fa`.

## Decisions

- Extend the existing `authn.Workflow`, `oidc`, `security`, `service` and
  `provider` responsibilities. One current-v2 durable authority owns native and
  browser sessions, policy, Apply, revocation and output. No new Go module,
  authentication broker, BFF, `lantern_auth`, database, clock or authority cache.
- Add a dedicated unary Connect native-auth service and an Apple Android HTTP
  callback on the existing certified public listener. Native auth is supported
  only with OIDC/current-v2; OFF and legacy-v1 cannot issue or admit this profile.
- Issue a random opaque native end-user Bearer credential with **fixed eight-hour
  absolute expiry**, reusing `security.MaxSessionLifetime`. No sliding expiry,
  Lantern refresh token, provider refresh-token retention or background renewal.
  Expiry requires another explicit login. Do not extend the existing lifetime cap.
- Google uses strict backend ID-token verification with a documented reusable
  bearer-input policy, not a fresh-per-login provider nonce. Apple uses a fresh
  transaction nonce and one Server-owned authorization-code exchange.
- Preserve signed `auth_time`, including absence and older values. Neither a
  plugin event, transaction, exchange, callback, session nor re-login creates
  fresh authentication. Existing operation-bound high-impact approval stays
  separate; native login never fulfills it.
- Only exact existing active `(iss, sub)` Principals can receive a session.
  Existing Role/Deny policy determines access. No signup, `implicitRole`, email
  linking, caller-supplied subject, or Google API authorization is added.

## Ownership and concrete extension points

| Owner | Work belonging here |
| --- | --- |
| `server/internal/oidc` | Shared bounded JWT/JWKS/signature/time primitives; explicit Google-native and Apple-native/web verification; Apple `client_secret_post` exchange; opaque native completion evidence; bounded one-use provider transactions. It does not issue Lantern sessions or decide Roles. |
| `server/internal/authn` | `Workflow.BeginNativeLogin` / `CompleteNativeLogin`, Apple callback staging, common current-session establishment, shared prepare/apply logout orchestration; concrete trusted credential producers. No HTTP, Connect, protobuf, persistence owner or worker. |
| `server/internal/security` | Committed issuer/client profiles and session profile, original evidence commitments, current admission, historical verification, Prepare/Apply/lineage/revoke, current self/issued-session output. No provider HTTP/plugin import. |
| `server/service` | Native Connect DTO validation/status/encoding, strict Apple form POST and fixed application return URI, header/TLS/proxy checks, output-context propagation. No duplicate provider/session decisions. |
| `server/provider` | Focused trusted config, one shared fetcher/key cache/secret registry, transaction/workflow/adapter construction, exact runtime certification and cleanup. |
| `sdks/dart` | Pure-Dart typed facade over the chosen wire contract, normal transport/deadline/cancellation/redaction, existing per-attempt `TokenProvider`. Generated classes stay private. |
| `sdks/dart/example` | Plugins, UI/cancellation, platform registrations, app-only secure credential storage, pending-login/account generations, cache/outbox ownership. |

Start from `authn/current_session.go:establishCurrentSession`, not a second
issuer implementation. Extract its common generate/Prepare/Apply/Refresh/Bind
sequence only when the native caller exists. A closed internal issuance input
selects browser versus native material; it cannot accept arbitrary claims or a
`verified` Boolean. Browser still gets session+CSRF cookies; native gets only its
opaque Bearer and non-secret metadata.

Use coherent paired files such as `oidc/native_login.go`, `oidc/apple_exchange.go`,
`oidc/native_transactions.go`, `authn/native_login.go` and
`service/security_native.go` only as their responsibilities warrant. Reuse
low-level bounded transaction/secret/hash helpers without merging browser PKCE
and Apple into a permissive bag of optional fields. Update the existing
`authentication_boundary_gate_test.go` for actual producer placement. Follow
one source/test pair; cross-package cases belong in `tests/integration/`.

## Trusted configuration

Add a bounded, canonically ordered `NativeClients` collection to the committed
`security.Issuer` record and its existing configuration/management codecs. Each
entry contains a unique opaque `id` (1–64 ASCII identifier characters), a closed
profile kind, exact client/audience fields, canonical public service origin,
and profile-specific non-secret callback/secret references. Limit to 16 entries
per Issuer, 16 exact Google presenter IDs per entry, and 512 bytes per client ID.
Duplicate IDs, unknown kinds/fields, invalid field combinations, wildcards and
empty trust values fail validation. Disabled/missing entries cannot be selected.
Empty `NativeClients` disables native login for that Issuer.

`service_origin` is a committed logical service/client binding. The SDK requires
it to equal its configured HTTPS endpoint origin, and current admission requires
the stored session to match its committed profile. It is not a separate HTTP
Host-based tenant boundary: aliases reaching the same configured authority do
not create independent credential namespaces. Use the existing certified TLS/
trusted-proxy ingress and separate authorities for independent trust domains.

This collection participates in `Issuer.ConfigRevision`, trust commitments,
image validation, copy/detach, canonical operations and policy changes. Adding
or widening a client profile is a trust change under the existing purpose
policy. Profile removal/disable or Issuer configuration change invalidates
affected old evidence/sessions through the existing revision/lineage path.
There is no independent runtime map that can silently override committed trust.

| Profile kind | Exact accepted inputs and destinations |
| --- | --- |
| `google_id_token` | Issuer **`https://accounts.google.com`**; RS256; exactly one `aud` equal to the configured backend Web client ID (`serverClientId`). If `azp` is present it must be in the explicit presenter-client allowlist. Absent `azp` is accepted only with that exact single audience. No multi-audience token in the initial profile. |
| `apple_native` | Issuer **`https://appleid.apple.com`**; RS256 ID token; exactly one audience equal to the registered native Bundle/App ID client ID; no authorization redirect URI and no Android-return fields. |
| `apple_android_web` | Same exact Apple Issuer and RS256; exactly one audience equal to the registered Services ID; exact registered HTTPS callback URI and fixed Android package/callback route; never a native Bundle ID substituted for the Services ID. |

Google also documents the bare Issuer `accounts.google.com`. This initial
contract deliberately supports the HTTPS spelling only: reject the bare
spelling, do not normalize it or merge two exact Principal identities. Document
that selected subset in setup and real-provider qualification. A Google
presenter allowlist is an issuer assertion policy, not device/app attestation;
an absent `azp` cannot prove which installed app requested the token.

Reuse safe-fetch DNS/TLS/redirect/size/key-cache limits. Native profile discovery
is selected only from registered trusted Issuer data. Pin Google's metadata
and JWKS to the official HTTPS origins and Apple's token/JWKS destinations to
`https://appleid.apple.com/auth/token` and `/auth/keys`; verify returned metadata
before any secret-bearing request. No request accepts issuer/JWKS/token URLs,
client IDs, redirect URLs, algorithms, a destination package or secret reference.
Keep browser `ValidateIssuer`/PKCE validation strict; add the native-only
validation branch so an Apple-native-only Issuer need not pretend to support
browser S256. Do not require dummy browser client/redirect/API-audience values
for a native-only Issuer. Disabled browser/API routes still fail closed.

Apple entries reference an operator-provided signed client-secret JWT. Load it
through the existing private-file `SecretRegistry`, bound to exact Issuer,
profile client ID and pinned token destination. Read each exchange so atomic
operator file replacement can update it; never silently reuse an expired secret.
The signing key and JWT creation/rotation are operator concerns outside this
implementation run. The JWT uses Apple's documented ES256 client-secret
contract; this is distinct from the RS256 identity-token algorithm. Do not store
its bytes/path in committed Issuer metadata, public DTOs, logs or build defines.
Any access/refresh token returned by Apple is bounded, used only for applicable
co-returned token checks, then discarded. No provider token enters shared graph
data or the session response.

## Wire contract selected before Dart consumers

Define `proto/graph/v1/native_auth.proto` with
`graph.v1.LanternNativeAuthService`. Use unary Connect POSTs, the normal protobuf
and Connect JSON codecs, existing strict-message validation and the same
certified HTTPS listener/gateway. All responses are `Cache-Control: no-store`.

| Method | Request | Result / rule |
| --- | --- | --- |
| `BeginNativeLogin` | Registered `issuer` selector and `client_profile_id` only. | `attempt_id`, private `completion_secret`, expiry, member/process affinity, profile kind and public plugin configuration; Apple additionally returns `state` and raw `nonce`, and Android's exact Services ID/callback. No identity or Role is supplied. |
| `CompleteNativeLogin` | `attempt_id`, `completion_secret`, and oneof Google `{id_token}` / Apple `{identity_token, authorization_code, state}`. | One opaque `credential`, `created_at`, `expires_at`, exact Principal identity, client-profile ID, credential-profile identifier, original optional `auth_time`, current authority profile/cut/binding. No provider token, CSRF secret or cached permission grant. |
| `GetNativeSession` | Empty; native session Bearer required. | Same non-secret session/Principal metadata, current explicit Roles and authority binding through `authn.InspectSession`; no credential re-disclosure. |
| `PrepareNativeLogout` | Empty; native session Bearer required. | Existing `CurrentReview` shape for revoking exactly the authenticated session. SDK wraps it in a detached typed `NativeLogoutReview`, without exposing generated types. |
| `ApplyNativeLogout` | The exact retained review. | Local-clear instruction plus optional currently disclosable original outcome. A missing outcome means unknown, never rollback or confirmed revocation. |

Reuse existing `LanternSecurityService.GetSecurityChangeStatus` for a retained
logout review; the native Dart facade exposes `getNativeLogoutStatus(review)`.
It must enforce current admission, exact actor/intent/profile and
`BindOriginalOutput`. A self-revoked credential cannot disclose a historical
result; a later valid session for the same Principal may query it. Do not create
a second outcome store or status credential.

Only Begin/Complete are credential-bootstrap exceptions in the auth interceptor,
matched by exact generated procedure constants. Other native methods require
the native session profile. They do not accept RFC9068, machine or cookie
substitution. Begin/Complete must not invoke the old `TokenProvider` or carry an
old account's Authorization header. Native routes reject cookie/Origin-bearing
browser requests and do not enable browser CORS credentials. The Apple callback
is its separate explicitly allowed browser form route. Ordinary data RPCs retain
their existing credential profiles plus the new native Bearer profile.

Cap each native Connect request at 64 KiB; ID tokens at 16 KiB each, code at
4096 bytes, state/nonce/attempt/proof at their fixed encoding bounds. Reject
unknown/duplicate structured fields, incorrect oneof/profile combinations,
multiple credential headers and unsupported media/encoding before provider work.
Use the existing safe errors: `invalid_argument` for shape, `unauthenticated`
for uniformly redacted credential/unknown Principal/expired or consumed attempt,
`failed_precondition` for an unsupported/disabled native runtime,
`resource_exhausted` for capacity/rate limits, and `unavailable` for authority,
provider or uncertain completion. Cancellation/deadline preserve uncertainty.
No raw provider body or exception is an error message. Bound a completion to
30 seconds (or the shorter client deadline), preserving the fetcher's existing
10-second request cap and per-host concurrency limits. No automatic retry of
Begin, Complete or Apply; explicit retained-review status/recovery is separate.

## Transaction and provider contracts

`provider.SecurityRuntime` owns one concrete bounded native transaction store
alongside its browser transactions; both borrow the existing qualified clock.
It has no journal, listener, goroutine or independent authorization decision.
Use a 10-minute absolute transaction lifetime and at most 1024 pending native
entries, with bounded expiry cleanup on access and existing public rate limits.
Generate independent 256-bit attempt, completion-secret, Apple state and Apple
nonce values. Encode Apple state as the existing `v2.member.process.random`
affinity syntax (at most 101 bytes); only the last component is the independent
random state. The public prefix is a routing hint, never authentication. A
gateway serving multiple members must route the Android form POST by this
bounded state hint to the Begin owner, just as browser callbacks retain their
owner affinity. Foreign-process state still fails exact owner validation. The native completion secret is returned only over Begin's TLS
response; it never goes to a provider, redirect, log or persistent app storage.
Store its digest. Bind the transaction to member/process incarnation, authority
domain/cohort/generation, Issuer configuration revision, selected client profile,
trusted service origin and the original start/expiry. There is no caller-provided
replacement-session digest.

Foreign node, restart, missing proof, changed configuration and expired attempt
fail closed. The Dart facade sends Begin's affinity in the single
`Lantern-Native-Affinity` header on Complete, to the same configured HTTPS
endpoint. Its canonical syntax is `v2.<nonzero uint32 member>.<43-character
canonical base64url process>` (at most 57 ASCII bytes). The gateway uses it only
as a routing hint; the destination owner still requires its live transaction
and private completion proof. Duplicate, empty, malformed or foreign-process
hints are rejected. Direct RPC clients with a fixed backend may omit the header;
an omitted hint never substitutes for transaction ownership. No redirect/failover
replays credentials to arbitrary nodes. In-memory transaction loss requires a
new user login attempt. The opaque transaction completion is usable only by
the trusted workflow, not as a decoded public evidence object.

### Google

Initialize app-owned `GoogleSignIn.instance` exactly once, with the configured
backend `serverClientId` and **no nonce** for this baseline profile. Reuse the
instance for `authenticate`, repeated login and lightweight/restored login.
Do not initialize again to install a Server nonce: the selected plugin accepts
nonce only in `initialize`, Android reuses that value, and iOS restoration does
not submit a new nonce. Native transaction proof controls delivery to the
initiating app; it does not turn a Google ID token into a fresh nonce-bound event.

Verify signature, exact Issuer/audience/presenter policy, bounded nonempty `sub`,
required signed `iat`/`exp`, optional `nbf`/`auth_time`, token type/algorithm and
all existing contradictory/future/expiry checks. No OAuth scopes, access token,
`serverAuthCode` or `at_hash` check requiring an unrequested access token is
needed. A signed `at_hash` is recorded as present-but-not-used under this distinct
ID-only profile, never labelled checked. Similarly an incidental signed nonce
can be recorded, but does not assert Server nonce verification in this profile.

Accept a still-valid ID token from another explicit repeated/restored login
attempt, including the same token bytes. Complete itself consumes its attempt
once and cannot issue twice. **The Google ID token remains a bearer input
replayable until its provider expiry**; do not claim global token replay
prevention, device binding or fresh authentication. TLS, app-local request
correlation, exact backend audience/presenter trust, redaction and short input
validity are the selected boundary. A captured token is not made safe by a fake
per-call nonce or an in-memory one-time token blacklist.

### Apple native and Android web

Pass Begin's random base64url nonce string to the plugin **unchanged**. Compare
the signed `nonce` to that exact string in constant time; never silently hash it
or borrow Firebase's raw/SHA-256 convention. State is a separate random value.
The app passes and checks state; the Server checks the same transaction state.
An absent nonce or a mismatched representation fails. Apple native completion
requires both nonempty identity token and authorization code.

Verify the plugin token before exchange, including exact Issuer, single native
or Services-ID audience, RS256/signature/time and nonce. If `c_hash` is present,
validate it against the supplied code; malformed/present-empty claims fail.
Atomically consume the native attempt **before** one code exchange. Send
`grant_type=authorization_code`, exact configured `client_id`, operator signed
`client_secret` in the form body and the code to the pinned token endpoint.
Only Android web includes the original exact `redirect_uri`. Do not send PKCE
or Basic auth and do not weaken the browser exchange path.

Verify the exchange ID token too: same exact Issuer, subject, audience and
transaction nonce, valid signed times/signature; check any applicable co-returned
token hash. The plugin's original signed authentication evidence remains the
login event; token exchange does not upgrade it. Retain a commitment to the
exchange and its verified token facts for current/historical evidence. Neither
an app DTO nor successful HTTP exchange alone can mint opaque completion.
Provider rejection, token/subject/client mix-up and response loss after sending
the code are terminal for that attempt. Never retry the code exchange on
RefreshRequest, replay, timeout or recovery. A new attempt requires a new
plugin authorization code; Apple's grant codes are one-use and short-lived.

For Android, mount `POST /auth/native/apple/callback/{client_profile_id}` with
the exact configured public HTTPS URI. Accept only bounded
`application/x-www-form-urlencoded` (64 KiB); reject duplicate state/code/token,
mixed query/body credentials, other methods and unsupported encodings. Verify
the profile, pending state, time/configuration and token nonce/signature before
staging the callback. Save only code/token commitments and atomically change
`pending -> callback_staged`; duplicate callbacks fail. Invalid or unverified
callbacks cannot stage an attempt or replace the current session. The pending
attempt remains bounded by its original expiry; an unauthenticated callback
cannot cancel another application's pending attempt.

Return only the validated, URL-encoded `code`, `id_token`, and `state` through
the stock plugin's fixed `signinwithapple://callback` Android intent, with the
operator-registered package; no caller-selected intent extra, fallback URL or
destination. Do not forward the provider's optional user/name/email blob. These
are sensitive short-lived provider inputs: suppress callback/Location logging,
use no-store and no-referrer, and document the bridge exposure. **No Lantern
credential or completion secret is ever in a redirect.** Do not disguise a
Lantern ticket as the plugin's documented Apple authorization code.

The stock Android parser does not validate state. The app therefore compares
the returned state and its pending account generation before calling Complete;
the Server requires the retained completion secret and exact staged code/token
commitments, consumes once, and performs the exchange described above. Forged
or late deep links never replace the current account. iOS native completion
uses its own pending transaction directly and has no web callback bridge.

## Session, credential evidence and current-v2 integration

Use `lnu1_` followed by canonical unpadded base64url of 32 cryptographically
random bytes. Parse exact length/alphabet; a malformed native prefix is rejected,
never retried as a JWT or machine token. Persist only a domain-separated SHA-256
digest. The prefix is routing syntax, not authentication. Raw credential exists
only in request/workflow memory and the successful app response/storage.

Extend `security.Session` with a closed credential profile (`browser_cookie`
or `native_end_user`) and native client-profile/service-origin binding. Native
sessions require empty CSRF fields; browser sessions require their existing
CSRF/origin proofs and cannot be used as native Bearers. The eight-hour deadline
is selected once from the qualified lower UTC bound at Prepare; final admission
uses both bounds. Provider-token expiry bounds login admission, not the already
issued Lantern session lifetime. No data RPC writes session activity/expiry.

Add native login and native-session provenance explicitly, with `EndUser` class
derived only from the trusted producer. A native provider event may authorize
only issuance, not data or management RPCs. An installed native session may use
ordinary authorized work/management; high-impact operations still need the
existing reviewed operation-bound proof. Do not grant native login input the
RFC9068 human classification or change machine eligibility. As with browser
login, successful native issuance may update the existing Principal's
`HumanIssuerConfigRevision`; it adds no Principal or Role and does not remove
the separate RFC9068 namespace-qualification requirement.

Extend the shared signed-token evidence with a **separately versioned native
variant**: native mode/provider profile, exact client-profile commitment, original
signature/key/Issuer/config/audience/claim facts, consumed transaction binding,
original transaction dates, explicit nonce policy/result and Apple exchange
commitment/verified response facts. Existing browser Code evidence continues
to require its nonzero PKCE and Code facts. Native Google carries no fabricated
exchange, PKCE, nonce-success or recent-auth fact. Apple carries no fake PKCE.
Version/domain-separate the native commitment; retain existing version-1
browser/access transcripts unchanged. Irrelevant variant fields must be absent.
`oidc` returns an opaque `NativeLoginCompletion`; only the concrete `authn`
producer can turn it into facts under a supplied `CurrentCredentialView`.
Zero values, JSON-decoded facts and exported identity constructors are not proof.

The native provider-event admission deadline is the earliest of original ID-token
expiry, transaction expiry and (for Apple) the exchange token's expiry. The
qualified lower bound must cover each signed start/consumed timestamp and the
upper bound must precede every deadline. Reuse bounded current-owner pacing
when a valid signed start is ahead of the lower bound; never change the claim,
sample host time as a fallback, or exchange the Apple code again while waiting.

Align all validation sites in the same Server change:

1. `authentication_evidence.go`, `authentication.go`, `session.go`, `issuer.go`,
   `image.go`: bounded native facts/profile and original signed-time semantics.
2. `control_current_credential.go`: fresh native-login facts and installed
   native-session facts; exact snapshot, revision, lineage, profile and expiry.
3. `control_origin_claim.go`, `control_apply.go`, `control_operation.go` and
   `control_projection.go`: historical claim/command validation, native issuance
   and revocation, same-current Principal/Role checks. A session cannot be issued
   from a native session credential or fabricated token fact.
4. `control_public_session.go`: shared issuance/confirmation with distinct
   evidence variants; native-specific validation, not removed browser guards.
5. `control_public_admission.go`: replace `Session != nil` as a proxy for browser
   authentication with explicit profile classification. Native must not inherit
   cookie/CSRF paths or browser-read-only status.
6. `control_public_output_decode.go` and output scopes: certify exact native wire
   response bodies, preserve the live output owner through marshaling and every
   final write. Keep current stream renewal/output checks unchanged.

Change `authorityAdmissionProfile().CredentialContract` to bind these new
evidence/session semantics. Retain the current-v2 public owner model and time,
consensus, custody and renewal rules; do not build a parallel engine. The exact
origin profile/protocol fingerprint changes, so older provisioning/peer history
cannot silently acquire native support. Update deterministic provisioning and
schema/digest fixtures coherently; test old/mixed fingerprints fail closed before
serving or advancing durable floors. Existing persisted cohorts need an explicit
reviewed provisioning/migration path, outside automatic startup; do not mutate
operator state or relabel historical evidence during this task.

The one issuance path is:

```text
consume native transaction -> genuine provider completion
 -> WithRequestCredential(current native producer)
 -> generate native credential -> PrepareSession(original fixed expiry)
 -> Apply(original S1Applied)
 -> RefreshRequest(same original provider event; no code exchange)
 -> BindIssuedSessionOutput(exact installed native session)
 -> certified native response with refreshed output context
```

Failures before or after Apply disclose no credential. After an Apply timeout,
post-Apply policy change, failed Bind or lost response, the session may exist;
there is no rollback assertion and no automatic repeat issuance. The raw
credential is not recoverable from the durable digest. The client labels the
attempt uncertain, discards its candidate, preserves its prior session if any,
and requires a deliberate new login. Unreachable sessions remain bounded by
the fixed expiry and existing session capacity/administrative revocation. Do
not keep an additional durable encrypted token-response cache just to replay
Complete. Reusing the consumed attempt never makes another session.

Logout reuses `PrepareSessionRevocation` and retained review/Apply/status logic.
Refactor only the transport-specific local-clear part of
`Workflow.ApplyLogout`: native clearing must not borrow `CurrentLocalCookieClear`
or a browser cookie proof. Bind a narrowly typed local-clear metadata result;
original outcome disclosure still requires RefreshRequest/BindOriginalOutput.
On a timeout, local credential removal and server revocation outcome remain
distinct. Administrative revoke, suspension, disabled Issuer/profile, Role/Deny
changes and unavailable/stale authority affect native RPC/streams through the
same committed state and finite output grants as existing callers.

## Lifecycle and provider disconnect

Keep resources in `SecurityRuntime`: shared Fetcher/KeyCache/SecretRegistry,
native transaction store, current owner and concrete workflow/adapters. Closing
stops admission, discards pending transactions and joins existing workers before
the established authorizations/fetcher/store/current-owner cleanup order. The
transaction store has no worker requiring an independent shutdown loop.
`App.Run`, abort `Close`, orderly `Shutdown`, `sync.Once`, certification and
the original owner/lease references remain authoritative. Cancellation must
release per-request capacity without erasing uncertainty after dispatch.

Lantern logout revokes a Lantern session; plugin `signOut` clears app/provider
local state; Google `disconnect` revokes its authorization; Apple consent revoke
and account deletion are separate provider operations. The baseline promises
no immediate provider-disconnect convergence. Previously issued Lantern
credentials can remain valid for their remaining eight hours unless current
Lantern revocation/policy acts sooner. On iOS any app-observed Apple state other
than `authorized` triggers local teardown and best-effort Lantern logout, but is
not trusted Server evidence. The pinned plugin exposes `authorized`, `revoked`,
and `notFound`; unsupported states or plugin errors also close local access.
No Android equivalent is invented.
After expiry the provider must authorize a new login.

Apple warns that acquiring ID tokens more often than daily can be throttled.
An eight-hour fixed session deliberately favors the existing bound over seamless
long-lived sign-in: re-login is user initiated, and throttling leaves the user
signed out. Do not loop interactive login on every launch or hide this limitation
with background refresh. Longer sessions/provider status convergence would be
a separately designed lifecycle change, not a reason to add refresh now.

## Pure-Dart and Flutter consumers

The inspected repository pins Flutter **3.44.6 / Dart 3.12.2** for the example
and Dart **3.11.0** as the SDK floor. Select exact app dependencies
`google_sign_in: 7.2.0` and `sign_in_with_apple: 8.2.0`; the latter requires
Flutter 3.44.0 / Dart 3.12.0 and therefore belongs only in the example. Resolve
and commit the actual transitive/platform lock under the repository pin before
claiming compatibility. No toolchain bump is selected. Pin-compatible source
inspection is design evidence, not a successful pub solve or platform build.

Expose typed `NativeLoginAttempt`, Google/Apple completion inputs,
`NativeSession`, `NativeLogoutReview` and narrow error/outcome types in
`lantern_client`. Reuse the client's invoker and HTTPS/deadline/cancellation
logic. Bootstrap calls explicitly suppress bearer injection and retries;
authenticated calls use the existing `TokenProvider`. Credentials/proofs have
redacted `toString` and are excluded from error metadata. Do not expose generated
clients/messages, Flutter, plugins, platform channels or secure storage from the
SDK. Regenerate all affected outputs with the existing generators.

The app owns a small session controller and plugin adapter with actual UI/test
callers; avoid a new generic authentication framework. Store only the Lantern
credential plus endpoint/exact identity/profile/expiry through an app-only
Keychain/Keystore-backed implementation; no provider tokens or pending proofs
in SQLite, plain preferences or logs. Select app-only
`flutter_secure_storage: 11.2.0`: its archive requires Dart >=3.8, Flutter >=3.19
and Android minSdk 24; the inspected Flutter 3.44.6 Gradle extension also selects
24, which the example already inherits. Use device-local Keychain accessibility,
disable credential backup/restore, and follow platform setup before validating
the resolved build. Keep the dependency outside the parent archive/runtime graph.

Each login/account/logout transition advances an app generation. Keep one
pending login; capture that generation for plugin/HTTP completions and reject
late completions after cancellation/account change. A new account is installed
only after valid native completion and session inspection. Prepare/retain old
logout review where possible, stop old foreground calls/subscriptions, then
remove old credentials and granting views. Every per-attempt TokenProvider is
bound to its immutable session generation: retrying old work must never fetch
the new account's credential. Create/close client instances as necessary rather
than putting a globally mutable token behind old queued work.

Keep a stable storage namespace for normalized endpoint, exact Principal and
native client profile; additionally bind active cache/subscription granting
views to the current authority profile/cut/credential binding. A new session
or policy cut invalidates the old granting views, not the retained mutation
identities. Preserve possibly dispatched mutation IDs and `mayHaveDispatched`
before sends; old account partitions stay dormant and status-first under their
original account and receipt scope.
Never flush another account's outbox, replace uncertain IDs, or replay an old
mutation under the newly signed-in account. Cancellation is not proof that a
mutation was never dispatched. Startup validates a stored Lantern session via
Server inspection; plugin restoration alone cannot activate cached authority.

In the current example, `OfflineDemoSession.wipeOnLogout()` calls
`repository.wipePartition()` and deletes outbox/operation rows too. Do not reuse
it for ordinary native logout, expiry or account switch. Quiesce with `close()`
and isolate the old database; reserve wipe for a separate explicit forget-device-
data action. A returning identical Principal resumes uncertain work status-first.

## Finite validation and implementation order

1. Implement committed client/session profiles, native evidence and exhaustive
   current/historical validation with paired tests. Update credential-contract
   fingerprint/fixtures and prove profile confusion/fabrication refusal.
2. Implement provider-specific OIDC adapters and bounded transactions; then
   Workflow issuance/revocation reuse and certified Connect/Apple callback
   adapters. Complete the Server wire contract and source tests **before** Dart
   facade/example work.
3. Generate consumers, add pure-Dart facade, then app-owned plugins/storage/
   controller/platform setup. Update ADR 0012, operator/setup docs, example and
   package documentation in the same cohesive implementation PR.
4. Resolve targeted review findings, freeze one coherent candidate, run the
   applicable full local gate, push, wait for required CI, squash/delete branch
   normally and verify all applicable post-main workflows at the merged SHA.

No duplicate review campaign is created by this checkpoint. Use targeted
security review for the new evidence/protocol boundary when the parent assigns
it; routine implementation/repair can run at the normal implementation effort.

| Focused test family | Finite required cases |
| --- | --- |
| Provider paired tests | Google exact HTTPS Issuer/single audience/allowed and wrong azp/signature/expiry; same token repeated/restored accepted without fresh auth; signed missing/old/future/null auth_time. Apple native/web mix-up; raw versus SHA nonce; code/token subject mismatch; optional hash; secret-post and conditional redirect; timeout/reuse gives one exchange. |
| Transaction/transport | Size/media/duplicate/unknown/oneof rejection; wrong secret/state/profile/incarnation; expiry/replay; Android staged callback and deep-link/app generation; missing or duplicate callback; no credentials in logs/errors; OFF/legacy disabled. |
| Genuine current evidence | Production opaque verifier/producer; decoded/zero/forged facts rejected; cross-use of cookie/native/machine/RFC9068 refused; unknown/suspended Principal; missing Role/Deny; fixed expiry; config and credential fingerprint mismatch; restart/profile persistence and no raw token in records. |
| Real Connect wire | One Google and each Apple profile happy path, authorized prefix and denied prefix; dropped Apple token response; lost Complete response after Apply; concurrent Complete once; post-Apply policy change/no secret output; logout loss/status; authority stale/unavailable; disabled Issuer, suspension, Role/Deny/session changes and active CDC/export output refusal with positive controls. |
| Dart/example | Format/analyze/test/docs/archive under existing gates; HTTPS/cancellation/redaction/one-attempt bootstrap; plugin initialize once; restored/repeated login; late callback, storage failure, logout uncertainty, account switch with in-flight calls/streams and an uncertain outbox mutation retaining status-first identity. |
| Hot path | Extend paired warm-admission benchmark and `testbed/bench/scenarios/current_authority_seam.sh` native-session scenario: no IdP/peer request, journal write or new time refresh on warm authenticated data access. Preserve existing thresholds; no new soak. |

Place composed wire cases in the existing current-security integration fixture
and execute them through its runner; plain `go test` skipping qualified bodies
is not acceptance. Existing deterministic provider fixtures prove source
contracts only. Use controlled-source **F** and normal configured-source **Q**
as separate evidence. This changes a credential/proof/composition boundary,
so Q is required before and after merge. Keep the current simplified CI and
local 77-step gate from `CONTRIBUTING.md`; do not restore removed OS/toolchain
matrices, manual transport comparisons, physical sleeps or redundant soaks.
This design document is outside the narrow README-only prose eligibility;
keep it in the implementation PR and qualify that candidate before any push.

Before any costly real-provider/device work, report and record the exact final
source/artifact hashes, selected device/OS/app/provider-client configuration,
cases, elapsed/run budget and required user action. Proposed affected matrix:
Google Android, Google iOS, Apple iOS, Apple Android. Per cell, select one
installed-device end-to-end login/authorized+denied RPC/logout/expiry case set;
add Google repeated/restored and Apple Android state/callback cases only where
relevant. Budget one execution per declared case set; rerun only after a
documented repair, invalid evidence or predeclared stability check. The actual
device/configuration and budget must be selected before launching that work.
Reuse valid #1610 evidence when exact source/environment boundaries permit.
No provider provisioning, private-secret inspection, deployment, tag, package
publication or device interaction is authorized by this design checkpoint.

## Independent exits and handoff

| Exit | Checkpoint status |
| --- | --- |
| Server contract decision | Recorded here before consumers; implementation pending. |
| Merged source/required CI | 0/3: Server, pure-Dart facade, Flutter example/docs/tests. Baseline CI is not this feature's CI. |
| Final exact-source functional acceptance | 0/1; run composed cases on the final source. |
| Real provider/installed platform evidence | 0/4: Google Android/iOS and Apple iOS/Android remain unqualified. |
| Publication | 0/1; separate authorization/schedule and release gates. |

No further user approval is needed to implement this selected contract. Actual
remaining external inputs are registered Google/Apple client profiles, a
Server-private Apple signed client secret, callback/application registrations
and selected devices/user interaction for the four real-provider exits. Those
do not block deterministic Server/Dart/example implementation. Do not close
#1750 or mark external exits complete merely because source/CI passes.

## Inspected primary references

- [Google backend ID-token verification](https://developers.google.com/identity/sign-in/ios/backend-auth)
  and [Google OIDC claims, including azp](https://developers.google.com/identity/openid-connect/openid-connect).
- [google_sign_in 7.2.0](https://pub.dev/packages/google_sign_in/versions/7.2.0)
  and [versioned initialize/authenticate source](https://raw.githubusercontent.com/flutter/packages/google_sign_in-v7.2.0/packages/google_sign_in/google_sign_in/lib/google_sign_in.dart).
- [Android 7.2.17 implementation](https://raw.githubusercontent.com/flutter/packages/google_sign_in_android-v7.2.17/packages/google_sign_in/google_sign_in_android/lib/google_sign_in_android.dart)
  and [iOS 6.3.6 implementation](https://raw.githubusercontent.com/flutter/packages/google_sign_in_ios-v6.3.6/packages/google_sign_in/google_sign_in_ios/lib/google_sign_in_ios.dart).
- [sign_in_with_apple 8.2.0](https://pub.dev/packages/sign_in_with_apple/versions/8.2.0)
  and its [changelog](https://pub.dev/packages/sign_in_with_apple/versions/8.2.0/changelog).
  The [exact 8.2.0 archive](https://pub.dev/api/archives/sign_in_with_apple-8.2.0.tar.gz)
  was inspected and matched pub.dev SHA-256
  `6d205ad2b30f95b231812d61f19b4a8df836f16ba9cde28735b3ac8ece9113f7`.
  Its platform-interface constraint is `^2.0.0`; the
  [2.0.0 archive](https://pub.dev/api/archives/sign_in_with_apple_platform_interface-2.0.0.tar.gz)
  matched `981bca52cf3bb9c3ad7ef44aace2d543e5c468bb713fd8dda4275ff76dfa6659`.
  Those exact sources confirm unchanged native nonce/state forwarding and
  Android code/id_token/form_post generation and code-only-required callback
  parsing without state validation. Resolve and commit the complete lock during
  implementation; archive inspection is not an installed-device qualification.
- [flutter_secure_storage 11.2.0 archive](https://pub.dev/api/archives/flutter_secure_storage-11.2.0.tar.gz)
  matched `d4e1fb6b2cb524868929e78dc0282fa000554b22060fb53789dc481c9fc95bb8`.
  Its `pubspec.yaml`, Android Gradle configuration and Keychain/backup setup were
  inspected. Use minSdk 24 from the build input rather than older README prose.
- [Apple token validation](https://developer.apple.com/documentation/signinwithapplerestapi/generate-and-validate-tokens),
  [Apple user verification/lifecycle](https://developer.apple.com/documentation/signinwithapple/verifying-a-user)
  and [official Apple metadata](https://appleid.apple.com/.well-known/openid-configuration).
  Metadata explicitly advertises RS256 ID tokens and client_secret_post; do not
  confuse the client-secret signing algorithm with the ID-token algorithm.
- [Existing workflow/lifecycle design](login-session-workflows.md),
  [ADR 0012](decisions/0012-oidc-prefix-rbac.md),
  [current authority](security-current-authority.md) and
  [validation/CI contract](../CONTRIBUTING.md).
