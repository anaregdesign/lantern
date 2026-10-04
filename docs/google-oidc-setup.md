# Google OIDC setup and provider qualification

Tracks #1657, #1602, #1604 and #1610. This is operator setup, not a record of
successful Google login. Creating a client, synthetic provider tests, and
mobile machine-credential tests do not complete real-provider acceptance.

## Check the recent-authentication prerequisite first

Lantern's adopted browser-session policy requires a verified, recent signed
`auth_time`. Authorization requests include PKCE S256, state, nonce, `max_age`
and `claims={"id_token":{"auth_time":{"essential":true}}}`. Missing or stale
evidence fails closed; token `iat`, callback time, consent and account selection
are never substitutes for user authentication time.

Google's [OIDC documentation](https://developers.google.com/identity/openid-connect/openid-connect)
describes `auth_time` as an optional claim that must be requested and enabled.
Its [Security bundle setup](https://developers.google.com/identity/siwg/security-bundle)
requires a published, verified OAuth app, then **Settings → Advanced Settings →
Session age claims**. An ordinary **Testing** client alone does not satisfy
that prerequisite. Google also documents that it does not support Google
Account reauthentication requests. Lantern's step-up redirect therefore cannot
promise to refresh an old Google session: the returned signed evidence must
still pass the Server's recent-authentication check.

Do not publish a Google app, claim provider acceptance, or relax that policy to
make a test pass. The operator must decide and complete provider prerequisites
separately. If the selected client cannot supply the required evidence, record
Google login as unsupported under this policy and keep the provider exit open.

## Create the OAuth Web client

1. Open [Google Auth Platform](https://console.cloud.google.com/auth/overview)
   in the intended Google Cloud project. No SQL datastore or new Lantern runtime
   service is required.
2. Complete **Branding** with the app name and operator contact information.
3. Choose the intended **Audience**. An organization's Internal audience is
   appropriate only when all intended users belong to that organization.
   Otherwise configure External and add the operator account as a test user
   while in Testing. This is client preparation; it does not meet the Security
   bundle prerequisite above.
4. In **Clients**, create an OAuth client of type **Web application**, with a
   clear name such as `Lantern local OIDC verification`. Do not substitute a
   mobile, service-account, or Desktop client for Server-owned code exchange.
5. Choose the exact HTTPS Admin origin used by the local test gateway before
   registering the redirect. The Server uses a distinct callback derived from
   SHA-256 of the exact Issuer. For Google the callback path is:

   ```text
   /auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38
   ```

   For example, **only if** the local HTTPS Admin gateway actually uses
   `https://localhost:6380`, register this exact Authorized redirect URI:

   ```text
   https://localhost:6380/auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38
   ```

   Replace the origin before saving when using another port or host. Do not
   register a generic `/auth/callback`, add a trailing slash, or send Google to
   the private peer listener. Server-owned code flow does not require exposing
   tokens through JavaScript or using Google's implicit flow.
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
assignments, prefix Deny, scoped CDC and replica admission. Include a missing
or stale `auth_time` refusal and preserve the control-node/step-up limitation.
The user performs the Google sign-in and any provider-owned MFA; an agent does
not collect their password or MFA code.

Public evidence records source and binary hashes, topology class, timestamps,
scenario verdicts and failure categories. Keep client configuration, tokens,
codes, cookies, account email/subject, private URLs and raw traces private.
Candidate/provider results remain separate from merged-source acceptance,
artifact publication and physical Android/iOS evidence. No merge, deployment,
Google app publication or SDK release is authorized by this runbook.
