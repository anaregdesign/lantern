# Login/session workflow extraction — #1751 design checkpoint

Design recorded **before source movement** on 2026-10-13; implemented in this PR.
Inspected source: `f57593de99d16051daefd295c37014d7d3c836bc`, fetched from current
`origin/main`. This document records the boundary and acceptance plan; qualification results
belong to the PR evidence. [#1751](https://github.com/anaregdesign/lantern/issues/1751)
is authoritative and blocks [#1750](https://github.com/anaregdesign/lantern/issues/1750).

## Decision and dependency sketch

Add one Server-internal package, `server/internal/authn`, for concrete login/session
workflows. The name describes authentication orchestration and credential
adaptation, not authorization policy. Reuse `server/service` for HTTP/Connect
adapters. Do not introduce a module, generic backend interface, framework, public
auth library, or an abstraction per flow.

```text
cmd/Wire
  -> provider.SecurityRuntime       trusted construction, certification, cleanup
       -> service                  HTTP/Connect adapters and private browser proof
       -> internal/authn           login/session workflows; Code/Bearer producers
       -> internal/oidc            configured IdP protocol and verification
       -> internal/security        existing durable/current authority owners

service -> authn -> oidc -> security
service -> oidc/security           narrow credential adapters and existing codecs
authn -> security                  admission, Prepare/Apply/revoke/output binding
security -X-> authn/oidc/service/provider
authn    -X-> service/provider/pb/Connect/net/http
service  -X-> provider
```

`authn.Workflow` is concrete. `NewCurrentWorkflow` and `NewLegacyWorkflow` receive
only the existing concrete owners needed by that profile. A current workflow
borrows `*security.CurrentAuthority`; a legacy workflow borrows `*security.Store`,
`*security.ManagementAuthorizations`, and the existing authority-check and
enforcement functions. The transport borrows the lease-expiry check. Both
profiles borrow the configured OIDC fetcher,
secret registry, verifier/key cache, login transactions and clock. These are
trusted composition dependencies, not client input. Do not open or bootstrap a
Store in either constructor. Reject incomplete/mixed enabled configurations;
retain explicit OFF handling in composition/adapters.

Keep profile-specific implementations in paired files in that package rather
than inventing interchangeable security engines. No new permission check, clock,
session lifetime, fallback, cache, worker, or persistence layer is introduced.

## Concrete API and caller responsibilities

The following is the selected API shape. Small signature adjustments during
implementation must preserve these input/output and ownership constraints.

| API / owner | Actual caller and responsibility |
| --- | --- |
| `authn.Workflow.BeginLogin(ctx, LoginRequest)` | Auth HTTP adapter after method/query/origin checks. Input contains bounded issuer/return/legacy step-up intent and an optional **already admitted** replacement session, not a caller-asserted identity or replacement digest. Resolve registered issuer, discover, revalidate the exact cut, create the existing one-use transaction, bind current login output. Return the existing `oidc.LoginStart`. |
| `authn.Workflow.CompleteCallback(ctx, CallbackInput)` | Auth HTTP adapter supplies raw bounded state, transaction-cookie value, callback path, issuer, code and provider-error input. Workflow owns Consume, trust recheck, one code exchange, verification, purpose branch or ordinary session establishment. Return `CallbackResult` plus error; result carries whether the transaction was consumed even on subsequent failure. That flag authorizes only clearing the transaction cookie. |
| `authn.Workflow` private session establishment helper | Receives genuine opaque Code completion/producer from the callback path. Owns secret generation, digest creation, current Prepare/Apply/Refresh/output-bind, or the existing legacy IssueSession sequence. Returns session material only after that profile's successful sequence. Keep this helper private until a real second trusted caller exists. #1750 extends it after separately defining the native profile; do not prebuild native APIs now. |
| `authn.InspectSession(ctx, admission, store, current, now)` | Both GET `/auth/session` and Connect `GetCurrentPrincipal` use the same validated session/principal read. Preserve admission/current-profile/store checks and final self-output checks. Return domain values for the existing response: identity, assigned roles and environment-lock metadata, original expiry/recent-auth facts and existing admission version/cut. Service owns all proto/DTO encoding, CSRF token mapping and response headers. Browser HTTP must no longer invoke a Connect RPC method to perform its workflow. |
| `authn.Workflow.PrepareLogout(ctx, admission)` | Current HTTP adapter, after strict profile/local/prepare/review validation and mutation proof. Return `security.CurrentReview`; service encodes it and binds current self output, preserving the original order. |
| `authn.Workflow.ApplyLogout(ctx, admission, review)` | Current HTTP adapter passes a **decoded existing** `security.CurrentReview`. Check actor, use request-owned producer, Apply once, preserve uncertainty. Return local-clear decision separately from any currently disclosable original result. Only RefreshRequest plus BindOriginalOutput permits result disclosure. Service maps DTOs and clears cookies. |
| `authn.Workflow.Logout(ctx, admission)` | Legacy HTTP adapter. Preserve the existing Store.RevokeSession and enforcement result, with no current-v2 promotion. |
| `authn.Workflow` purpose begin/read/navigation methods | Only the existing management callbacks that share the login transaction and purpose callback move. Use `security.ManagementBinding` / `CurrentReview` / `AuthorizationStart` / `AuthorizationStatus`; return a ticket/affinity and login start, not HTTP objects. Service constructs navigation URLs, parses paths/affinity and emits the existing fixed HTML. Keep unrelated management operations in their current service/security owners. |
| `authn` private Code/Bearer producer with concrete factory | Code takes only `oidc.LoginCompletion` + `oidc.CodeTokens`. Bearer takes the raw credential after service validates the single bounded Authorization header. Verification uses the supplied opaque `security.CurrentCredentialView`; the factory never accepts TokenAuthenticationEvidence, claims, identities or `verified` flags. Preserve machine-token profile selection and all verification-time behavior. |
| `service` private browser producer | Constructed only after exact origin/proxy/route/cookie/CSRF checks. Retains detached private raw values and the permitted read-only route class, then verifies native Session/CSRF digests against the supplied current view. This is an intentional transport-specific implementation of the **existing** `security.CurrentCredentialProducer`; no exported raw-facts or checked-boolean constructor. |

The existing `provider.NewCurrentCredentialRuntime` has actual external-package
test callers in `control_genuine_producer_gate_test.go` and
`control_container_gate_test.go`. Keep it as a small construction facade that
composes these **same production** Code/Bearer and browser producers. It must not
embed a partially initialized `SecurityRuntime` or introduce a parallel verifier.
Existing public `SecurityRuntime` handler/authentication methods may remain thin
forwarders for real callers, but contain no moved login/session decisions.

Use `service`'s concrete authentication/browser adapter to retain transport
credential parsing and private request evidence. Legacy browser admission is
part of that trusted cookie adapter; current admission still uses
`WithRequestCredential`. Keep original bearer/machine/session classification,
deadline and request-evidence tests connected to the adapter. A legacy purpose
completion helper may take the actual opaque `oidc.VerifiedIdentity` directly
inside the workflow instead of exposing the old private context-evidence struct.
It must still check Code mode, operation flow and exact authorization ID. Never
export that struct just to enable the move or tests.

Errors from `authn` use narrow internal error categories or existing security errors;
they do not expose Connect codes or raw provider responses. The transport maps
each existing phase/profile to its unchanged status and fixed safe response.
Keep a characterization table where the same underlying failure currently has a
different legacy/current status. Cancellation, readiness failure and OFF remain
distinct. Do not normalize these distinctions during extraction.

## Trust and output invariants

1. `VerifiedIdentity`, `LoginCompletion`, `CodeTokens` and `CurrentCredentialView`
   retain their existing private representation. Zero/decoded/fabricated values
   are not evidence. `CurrentCredentialFacts` is already exported but is detached
   trusted-producer output, not a public authorization constructor. Production
   implementers remain an explicit bounded set in authn/service.
2. Current ordinary callback sequence remains exactly:
   `Consume -> ExchangeCode once -> Code producer -> WithRequestCredential ->
   generate secrets -> PrepareSession -> Apply(original S1Applied) ->
   RefreshRequest(same original Code event) -> BindIssuedSessionOutput ->
   session/CSRF cookies and redirect`.
   No pre-Apply admission or workflow success flag substitutes for output binding.
   Return the refreshed context with successful session output; adapters retain
   the current output owner/context through marshaling and writing.
3. A failed post-Apply Refresh or Bind returns **no session material**. The
   operation may already be committed: do not retry issuance, exchange Code again,
   claim rollback, or clear an existing session as an invented repair.
4. Consume precedes all provider work. A successful Consume must clear the
   transaction cookie even if a later exchange/verification/purpose step fails;
   failed Consume must not acquire that side effect. Use the result's consumed
   marker solely for this transport effect, never for admission. Pending HTTP
   headers are not published by the workflow.
5. Purpose completion is a separate branch. Reject the attempt on failure until
   approval; preserve current `CompletePurpose` then
   `BindPublicMetadata(CurrentPurposeReturn)`. It never establishes/replaces an
   ordinary session or writes session/CSRF cookies. The existing profile-specific
   fixed HTML remains in service.
6. Preserve original signed `iat`/`nbf`/`exp`/missing or old `auth_time`, Code
   consumed time, and the exact legacy time representation used in SessionRequest
   commitments. Timers only pace; current decisions use qualified owner bounds.
   No callback/renewal/session timestamp creates recent authentication.
7. Browser proof is not a user DTO. Public `CurrentBrowserReadProducer` remains
   limited to GET `/auth/session`; the private route adapter additionally permits
   the existing read-only GET `/auth/login` and `/auth/operations`. Mutations
   require separate exact Origin/CSRF headers. Preserve duplicate rejection,
   trusted-proxy rules and Bearer substitution refusal. No unrestricted read-only
   boolean reaches a public producer constructor.
8. Current local-only logout is transport cookie deletion with strict request/CSRF
   validation and `BindPublicMetadata(CurrentLocalCookieClear)`, not revocation.
   Normal current logout preserves the client's retained review and uncertainty;
   a self-revoked credential cannot disclose an original cluster result under its
   old admission. Legacy logout remains its existing profile.
9. `CurrentOutput`, resource-specific data authorization, commit/output checks,
   scoped streams, and legacy snapshot admission remain at their existing owners.
   Moving browser wrappers must preserve snapshot/output wrapper order. If the
   legacy snapshot transport helper moves to service with its interceptor, move
   its paired tests unchanged; do not redesign data authorization.

## Lifecycle ownership and wiring

| Owner | Must remain owned here |
| --- | --- |
| `provider.SecurityRuntime` | Trusted config/profile selection; OIDC fetcher, secrets, keys, verifier, login transactions; legacy authorizations; NativeStore or CurrentAuthority; one process-incarnation value; concrete workflow/adapter construction; certified exact data-runtime references. |
| `internal/oidc` | Provider discovery/exchange/verification and existing ephemeral one-use transaction state. No Lantern authorization/session store. |
| `internal/security` | Principal/Role policy; durable sessions; current time/custody/M/P/B/private listener; admission, Prepare/Apply/revoke; output owner and finite output grants. |
| `authn` and service adapters | Borrow the constructed owners. No Close/Shutdown, goroutine, new listener, or independent persistence. Per-request secret/result/evidence lifetime only. |
| `cmd.App.Run` | Join public/private serving and workers, distinguish normal drain from failure, call security orderly shutdown only on success, then close data durability owner. |

Preserve `NewSecurityRuntime` failure defer and Wire cleanup calling abort
`Close()`. `finish` order remains authorizations -> fetcher idle connections ->
native store -> current owner. Previous cleanup failure forces current abort.
`sync.Once` prevents an earlier abort becoming a successful orderly shutdown.
`Shutdown()` remains the error-returning normal App path after all worker joins.

`OpenCurrentAuthority` still opens its private listener/time/custody before public
readiness. `NewPublicSecurityCertified` still validates the exact data, security,
peer, profile and TLS/gateway combination; `NewPublicLanternListener` still needs
that marker and binds the same current output owner. Do not certify arbitrary
workflow/adapter replacements or create a second time source. Preserve the one
startup-generated member/process affinity across all handlers and workflows.

Regenerate `server/cmd/wire_gen.go` using `go tool wire ./cmd` from `server` if
provider wiring changes. Never hand-edit it. A workflow assembled inside the
existing provider does not by itself require an additional Wire provider.

## Source/test movement and bounded verification

- Split `provider/security_browser_routes.go`, `security_current_browser.go`,
  `security_browser_boundary.go` and their paired tests by actual responsibilities:
  workflow code/tests under authn; decoding/cookies/status/origin tests under
  service. Use coherent `workflow`, `browser_login`, `current_session`,
  `legacy_session`, `purpose` and `current_credential` files only as warranted;
  avoid a file per wrapper. Keep one matching test file per source.
- Move Code/Bearer producer verification and qualified-start tests to authn;
  browser/header proof tests to the service adapter. Move/split legacy request
  evidence tests with their real producer; preserve redaction and failed-admission
  nonpublication assertions. Keep runtime/certification tests in provider/cmd.
- `GetCurrentPrincipal` is the shared inspection caller. Its role list,
  environment-owned assignment metadata, order, version/cut, CSRF bootstrap and
  final current self-output check must remain identical. General list/change
  management RPCs and resource data authorization stay outside this extraction.
- Add a small import/producer-placement check in the new package's paired test
  (or a genuine cross-file boundary gate). Existing dedicated OIDC/security
  placement tests were not found; the Go SDK module gate alone is insufficient.
  Assert forbidden production back edges/transport imports and the actual bounded
  production producer locations. Do not restrict legitimate black-box test imports.
- Add targeted characterization at the extracted callback boundary for no session
  material/cookies when current Refresh/Bind refuses, plus transaction-cookie
  clearing on post-Consume failure if the moved coverage does not assert it.
  Use concrete owner fixtures or the existing real-wire fixture; do not add a
  generic authority interface or production fault hooks merely for a test.

Existing exact acceptance to retain:

| Area | Tests / evidence |
| --- | --- |
| Opaque genuine OIDC evidence | `internal/oidc/authentication_evidence_test.go`, `login_transactions_test.go`, verifier and code-exchange paired tests. |
| Browser OFF/session/logout/origin/CSRF/expiry | Current `provider/security_browser_routes_test.go`, `security_browser_boundary_test.go`, `security_current_browser_test.go`, `security_current_credential_test.go`, moved with their implementations. |
| Real wire legacy | `tests/integration/auth_test.go`: `TestAuth_OIDCBrowserSessionRealConnect`, `TestAuth_OIDCBrowserLoginFailuresRealConnect`, `TestAuth_OIDCBrowserLoginAndStepUpPurposesRealConnect`. |
| Apply vs publication | `internal/security/control_public_session_test.go:TestCurrentPublicSessionRequiresAppliedThenFreshAdmission`; `control_public_admission_test.go:TestCurrentPublicAdmissionDoesNotRelabelAfterApply`; current output scope/writer/interceptor tests. |
| Real Code/current producer | `internal/security/control_genuine_producer_gate_test.go:TestCurrentAuthorityGenuineProducerGate`; retain the actual production facade path. |
| Composed current browser/purpose | `tests/integration/current_security_gate_test.go:TestCurrentSecurityPublicNativeGate`, especially `browser Code one exchange and CSRF bootstrap` and `operation-bound real Code approval`. Covers foreign node/restart/replay refusal, replacement, bootstrap, logout and post-revoke disclosure. |
| Construction/custody/cleanup | `provider/security_runtime_test.go`, `security_current_runtime_test.go`, `security_workers_test.go`, `public_security_test.go`, `public_listener_test.go`, `cmd/server_test.go`, `cmd/wire_test.go`. |

During edits run those affected paired tests and the three legacy real-wire tests;
inspect that selected tests actually execute. The current native public gate can
skip under a plain `go test`: use the existing qualification runner, not a skipped
test as acceptance. The repository's full local plan already runs current SDK4
F/Q and scoped Rust in `rust-oidc-scoped-wire`; do not duplicate it as a new campaign.

Before each source push use CONTRIBUTING's full component gate, with frozen
Node/Admin installs completed before root Go traversal. The existing 76-step
`local_gate_plan.py` / `local_gate_session.py` runner is available. Only the live
runner may carry eligible unchanged component results; historical CI/memory cannot
replace local evidence. Acquire the shared host validation slot and configure the
repository-pinned tool versions before that work. Tools are present on this host;
version pins and slot availability are implementation preflight, not yet qualified.

Current `current_qualification_scope.py` treats Server changes as Q-required.
Run required F and configured-source Q before and after merge through the existing
gates. Required contexts include Build & Test, Lint, Proto (buf), govulncheck;
wait for all applicable workflow gates on the final candidate. Use normal squash
merge/delete-branch, then verify post-main CI at the exact merged SHA. No admin
bypass, direct main push, device/real-provider campaign, soak test, release or
deployment is added by this refactor.

## Documentation and follow-through

Update the placement comment on `security.CurrentCredentialProducer`,
`server/README.md` package/evidence sections, and relevant implementation-placement
text in ADR 0012 / `docs/security-current-authority.md`. Record #1750's entry point
as authn's trusted workflow plus a future distinct provider/profile adapter; do
not add a native input, native token acceptance, refresh, Dart API or schema now.

No unresolved security-design blocker was found. The highest implementation risks
are retaining error/side-effect ordering, keeping the same owner/output context,
and moving paired white-box fixtures without widening trusted construction APIs.
Implementation follows this boundary. Paired transport tests moved to service;
qualified Code-start tests moved to authn. The genuine-producer gate also calls
the concrete workflow with an absent output recipient, requires the session to
have been Applied, and checks that failure returns no session material and no
second Code exchange. The architecture gate checks production import and producer
placement. Existing composed browser and lifecycle gates remain authoritative.
No device/provider campaign or additional soak is introduced.
