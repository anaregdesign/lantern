# Login/session responsibilities

[#1751](https://github.com/anaregdesign/lantern/issues/1751) extracted login and
session orchestration into `server/internal/authn`. #1750 subsequently added
native registration/session flows through the same concrete owners. #1820
retires the alternate fixed-writer workflow; OFF and one OIDC composition remain.
This document describes the resulting ownership and evidence boundaries.

## Construction and dependencies

`provider.SecurityRuntime` constructs the configured OIDC fetcher, secret
registry, verifier/key cache, browser/native transactions, credentials and one
`security.CurrentAuthority`. `authn.NewWorkflow` borrows those concrete owners
and the same process/member affinity. It does not open authority storage,
bootstrap policy or select an interchangeable authentication engine. OFF is
handled explicitly by composition and transport adapters.

```text
cmd/Wire -> provider.SecurityRuntime
              -> service          HTTP/Connect and private browser proof
              -> internal/authn   login/session and credential orchestration
              -> internal/oidc    IdP discovery/exchange/verification
              -> internal/security quorum/time/custody/policy/output owners

service -> authn -> oidc/security
security -X-> authn/oidc/service/provider
service/authn -X-> provider
```

Keep responsibility-based paired source/tests. No generic authority interface,
caller-asserted verified facts, second clock, fallback, persistence engine or
public authentication library is needed.

## Workflow and transport

| Owner / operation | Responsibility |
| --- | --- |
| `Workflow.BeginLogin` | Resolve the exact registered Issuer, discover, revalidate the original cut, create the existing one-use transaction and bind login output. Input contains bounded Issuer/return data and an already admitted replacement session. |
| `Workflow.CompleteCallback` | Consume the exact transaction, recheck trust, exchange Code once, verify genuine evidence, then branch to purpose approval or ordinary session establishment. A consumed flag permits transaction-cookie clearing only. |
| Private session establishment | Generate secrets/digests, Prepare/Apply the original session operation, Refresh using the same Code event and bind issued-session output before exposing session material. Browser and native flows retain their distinct registered transport contracts. |
| `InspectSession(ctx, admission, current, now)` | Shared browser/Connect inspection. Check the actual owner/profile and final self-output binding; return identity/Role/lock/expiry data. Service owns DTO/protobuf encoding and CSRF headers. |
| `PrepareLogout` / `ApplyLogout` | Preserve the retained full review and original ID, Apply once, and retain uncertainty. Local cookie clearing is separate from currently disclosable original revocation proof. |
| Purpose methods | Bind final reviewed intent, original ID, actor and full current cut. Preserve qualified signed-event navigation timing and purpose-only callback behavior. Return domain tickets/affinity, never HTTP objects. |
| Private Code/Bearer producers | Accept genuine opaque Code completion/tokens or a bounded raw credential. Verify against the supplied opaque current credential view; no public constructor accepts claims, identities, evidence DTOs or a verified flag. |
| Private browser producer | Service owns exact origin/proxy/route/cookie/CSRF checks and detached request evidence. Read-only route scope stays bounded; mutations cannot substitute a Bearer or an unrestricted read-only Boolean. |

`provider.NewCurrentCredentialRuntime` remains a small construction facade for
real external-package conformance callers. It composes the same production
Code/Bearer/browser adapters, opens no authority and never embeds a partial
SecurityRuntime or parallel verifier.

Errors use narrow internal categories. Service maps the existing phase to its
fixed safe HTTP/Connect result without exposing raw provider responses. OFF,
authentication refusal, readiness failure and cancellation stay distinct.

## Trust and output

- `VerifiedIdentity`, `LoginCompletion`, `CodeTokens` and credential views retain
  private representation. Zero/decoded/fabricated values are not evidence.
- Ordinary callback order remains Consume, one Code exchange, genuine producer,
  request admission, secret generation, Prepare, original APPLIED result,
  Refresh for the same event, issued-session output binding, then cookies/redirect.
- Ordinary login/replacement preserves unknown or older authentication time.
  Replacement, refresh and callback time never manufacture a signed event.
  Trust changes and effective management expansion require operation-purpose
  proof of the exact final review.
- Failed replacement or purpose approval preserves the old usable session.
  Purpose completion branches before ordinary session issuance/cookie writes.
- Local-only logout deletes validated local cookies with its metadata binding.
  Normal revocation retains original result reconciliation. A self-revoked old
  admission cannot disclose cluster proof; a valid independently authorized
  credential may reconcile the retained original operation.
- Resource-specific data admission, immutable output grants, commit/output
  checks, scoped streams, native time and original-H custody remain at their
  actual security/data owners. A workflow success flag cannot replace them.

## Lifecycle

The workflow closes and joins its own native registration calls/proofs. It
borrows durable authority and OIDC resources. Runtime cleanup closes workflow
registration work, native login transactions, fetcher idle connections and then
the actual authority. Cleanup failure selects abort. `sync.Once` prevents an
abort from later becoming successful orderly shutdown.

`App.Run` joins owned serving/workers and invokes orderly security Shutdown
only on successful drain, before data durability cleanup. `OpenCurrentAuthority`
opens private time/custody/listener state before public readiness. The public
certification marker binds exact data, security, peer, original profile and
TLS/gateway owners; it cannot certify arbitrary workflow replacements.

## Evidence

Paired authn tests cover genuine credential and qualified Code-start behavior;
paired service tests cover HTTP decoding, cookies, origin/CSRF and self-output.
Current security tests retain Apply-versus-publication, genuine production
producer, admission, original result, revocation and bounded-output checks.
Root browser/management/data/receipt cases use independent original provisioning
and the same production public composition. The native SDK4 gate retains its
three-voter/two-origin owner and genuine Code/purpose/credential cases.

Controlled-source functional-validation and configured-source-integration runs must record actual execution;
a skipped plain `go test`, private-input export or parser-only fixture is not
acceptance. Follow the then-current repository qualification policy on the frozen
candidate and merged main. Real-provider, physical-device, publication and
deployment evidence remain separate; this retirement authorizes none of them.
