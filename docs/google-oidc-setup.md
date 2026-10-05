# Google OIDC setup and provider qualification

Tracks #1672, #1609, #1658, #1657, #1602, #1604 and #1610. This is operator setup, not a record of
successful Google login. Creating a client, synthetic provider tests, and
mobile machine-credential tests do not complete real-provider acceptance.
The operator Web client has already been created. The prior real Google trial
ended; the actual Server/IdP/Admin fixture was stopped after its short-lived
certificate expired. Neither fact completes the pending real-provider/human or
actual combined UI exit. Reuse the existing client for separately approved
work; this guide does not authorize recreating clients or changing credentials.

## Ordinary login and approved ordinary management

Ordinary login uses Authorization Code, PKCE S256, state, nonce and verified
ID tokens, then checks the current registered Issuer and Principal. It accepts
existing Google SSO without requiring the optional `auth_time` claim. A missing
claim stays unknown; an older signed value stays unchanged. Normal login and
session replacement do not request `max_age`, `prompt=login` or essential
`auth_time`. Neither operation makes old or unknown evidence recent. Future
or contradictory signed authentication times are rejected.

#1672's approved target allows ordinary end-user management with valid OIDC
authentication and current explicit `security.manage`, including missing/older
signed `auth_time`. Bootstrap may create a mutable scoped data Role and assign
it to the exact verified identity without replacing its environment-owned
`security_admin` assignment. Bootstrap grants no data by itself. Data grants
alone do not need additional authentication or extra initial provisioning.
Current credential/session expiry, policy admission, CSRF, CAS, env-owned locks
and administrator invariants remain required. This policy is not yet implemented
by merged fixed-writer source `93d53789`.

Trust changes and effective `security.manage` expansion require per-operation
reauthentication; removal of a Deny/Role/assignment can expand that authority.
The [operation/actor matrix and proof review](decisions/0012-oidc-prefix-rbac.md#management-operation-and-actor-policy)
are provider-neutral. Machines may perform authorized reference/status reads,
but no management mutation. Ordinary Google login and actual provider-neutral
ordinary-management evidence remain separate records.

## Historical step-up and optional Google extensions

At `93d53789`, Apply, Store management, ValidateIssuer and explicit step-up
enforce a five-minute signed authentication event. Step-up requests `max_age=0`,
`prompt=login` and `claims={"id_token":{"auth_time":{"essential":true}}}`.
Missing/old evidence fails step-up without replacing the old usable session.
This historical flow does not implement the approved operation-bound proof.
Token `iat`, callback time, consent and account selection remain invalid
authentication-time substitutes after the ordinary gate is removed.

Google's [OIDC documentation](https://developers.google.com/identity/openid-connect/openid-connect)
describes `auth_time` as an optional claim that must be requested and enabled.
Its [Security bundle setup](https://developers.google.com/identity/siwg/security-bundle)
requires a published, verified OAuth app, then **Settings → Advanced Settings →
Session age claims**. These extra claims, Security bundle and their Google app
publication/verification are optional and outside baseline implementation,
acceptance, Issue-closure and release gates. A **Testing** client can prepare
ordinary login and does not have to enable these extensions for ordinary
management. Google's documented OIDC `prompt` values do not establish support
for Lantern's historical `prompt=login` step-up request. Do not assume that a
redirect refreshes an old Google session; qualify the actual returned signed
evidence against the selected high-impact proof contract separately.

Google app publication/verification is a separate operator decision. If a
selected optional/high-impact flow cannot supply its required evidence, record
that flow as unavailable; do not make it an ordinary-login/ordinary-management
or Google Security bundle baseline prerequisite. Qualifying high-impact
protection remains separate from verifying ordinary Google compatibility.

## Configure or inspect the OAuth Web client

1. Open [Google Auth Platform](https://console.cloud.google.com/auth/overview)
   in the intended Google Cloud project. No SQL datastore or new Lantern runtime
   service is required.
2. Complete **Branding** with the app name and operator contact information.
3. Choose the intended **Audience**. An organization's Internal audience is
   appropriate only when all intended users belong to that organization.
   Otherwise configure External and add the operator account as a test user
   while in Testing. Security bundle is a separate optional extension.
4. In **Clients**, use an OAuth client of type **Web application**, with a
   clear name such as `Lantern local OIDC verification`. Do not substitute a
   mobile, service-account, or Desktop client for Server-owned code exchange.
5. Choose the exact HTTPS Admin origin used by the local test gateway before
   registering the redirect. The Server uses a distinct callback derived from
   SHA-256 of the exact Issuer. For Google the callback path is:

   ```text
   /auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38
   ```

   For example, **only if** the approved local HTTPS Admin gateway actually uses
   `https://lantern-test.replary.com:17443`, its exact Authorized redirect URI is:

   ```text
   https://lantern-test.replary.com:17443/auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38
   ```

   Replace the origin before saving when using another port or host. Do not
   register a generic `/auth/callback`, add a trailing slash, or send Google to
   the private peer listener. Server-owned code flow does not require exposing
   tokens through JavaScript or using Google's implicit flow.
   The proposed trusted origins, certificate/renewal and local resolution choices
   remain [fixture preparation](oidc-operations.md#trusted-https-fixture-preparation),
   awaiting individual approval; this example is not an installed redirect.
6. Save the client ID and download the client configuration into an operator
   private directory outside the repository. Do not put client secrets in
   Issues, chat, SPA/Vite variables, SDK binaries, shell arguments, logs or
   uploaded evidence.

## Bind the private secret and verified administrator

Use the canonical Issuer `https://accounts.google.com` and algorithm `RS256`,
as advertised by [Google Discovery](https://accounts.google.com/.well-known/openid-configuration).
The exact `sub` from a signature-, nonce-, audience- and Issuer-verified ID
token identifies the administrator. Email and Workspace domain are not Role
grants, account-linking rules or substitutes for `sub`.

Copy only the `web.client_secret` value into a separate absolute regular private
file with no newline. The native opened-file privacy contract is documented in
[ADR 0012](decisions/0012-oidc-prefix-rbac.md). On Unix use an operator-owned
directory with mode `0700` and a new secret file with mode `0600`; on Windows
use the documented owner/protected-DACL contract. The downloaded JSON is not
the raw secret file consumed by the Server.

These are the provider-specific pieces of the configuration; they are **not**
a complete production or HA configuration:

```text
LANTERN_AUTH_MODE=oidc
LANTERN_OIDC_ADMIN_ISSUER=https://accounts.google.com
LANTERN_OIDC_ADMIN_SUBJECTS=["<verified exact Google sub>"]
LANTERN_OIDC_CLIENT_ID=<Web client ID>
LANTERN_OIDC_ALGORITHMS=["RS256"]
LANTERN_OIDC_BROWSER_ORIGIN=<exact HTTPS Admin origin>
LANTERN_OIDC_REDIRECT_URI=<origin plus the Google callback path above>
LANTERN_OIDC_SECRET_REF=google_web
LANTERN_OIDC_SECRET_BINDINGS={"google_web":{"issuer":"https://accounts.google.com","client_id":"<same Web client ID>","token_origin":"https://oauth2.googleapis.com","path":"<absolute private raw secret file>"}}
```

Complete native durability, generation/writer keys, bootstrap revision, TLS,
qualified clock and other mandatory settings through the existing
[operations runbook](oidc-operations.md) and [ADR](decisions/0012-oidc-prefix-rbac.md).
Do not use an unverified JWT decoder, a guessed subject, email bootstrap,
anonymous enrollment or a fabricated privileged session to obtain first-admin
access. Subject discovery must be an operator-owned verified code exchange
before enabling that bootstrap binding; if that binding is not yet available,
provider setup remains incomplete.

The required Lantern API audience is a distinct Server configuration. Google
access tokens issued for Google APIs and Google ID tokens are not Lantern's
RFC 9068 API Bearer profile. Browser login creates the Server's own HttpOnly
session; native SDK/automation callers use their independently supported
credentials and Role assignments. Do not broaden the Bearer verifier to accept
Google API tokens simply because browser login uses Google.

## Record actual provider results

From a clean exact candidate, verify the real callback/session flow, CSRF,
unknown/revoked users, security-admin without graph access, Role edits and
assignments, prefix Deny, scoped CDC and replica admission. Record ordinary
login with missing/old `auth_time`, unchanged recent-auth eligibility after
ordinary rotation, and successful ordinary data-Role creation/exact self-
assignment after #1672. Verify allowed in-prefix CRUD and outside-prefix
rejection without implicit grants. Retain historical five-minute rejections
as source-specific evidence. Test invalid/future/contradictory evidence and
the selected operation-bound high-impact/machine restrictions separately;
retain current fixed-writer routing and Google reauthentication limitations.
No current-source API success or UI contract fixture proves the pending target.
The user performs the Google sign-in and any provider-owned MFA; an agent does
not collect their password or MFA code.

Public evidence records source and binary hashes, topology class, timestamps,
scenario verdicts and failure categories. Keep client configuration, tokens,
codes, cookies, account email/subject, private URLs and raw traces private.
Candidate/provider results remain separate from merged-source acceptance,
artifact publication and physical Android/iOS evidence. No merge, deployment,
Google app publication or SDK release is authorized by this runbook.
