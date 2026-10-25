# Google OIDC setup and provider qualification

Tracks #1672, #1609, #1658, #1657, #1602, #1604 and #1610. This is operator setup, not a record of
successful Google login. Creating a client, synthetic provider tests, and
mobile machine-credential tests do not complete real-provider acceptance.
The operator Web client has already been created. The prior real Google trial
ended; the actual Server/IdP/Admin fixture was stopped after its short-lived
certificate expired. Neither fact completes the pending real-provider/human or
actual combined UI exit. Reuse the existing client for separately approved
work; this guide does not authorize recreating clients or changing credentials.

Start with the [placeholder configuration files](../deploy/examples/auth/README.md)
for Server settings, OAuth clients and Android/iOS registration. Keep all actual
validation-specific values, including normally public client IDs, outside Git.

## Separate ordinary management from operation reauthentication

Ordinary login uses Authorization Code, PKCE S256, state, nonce and verified
ID tokens, then checks the current registered Issuer and Principal. It accepts
existing Google SSO without requiring the optional `auth_time` claim. A missing
claim stays unknown; an older signed value stays unchanged. Normal login and
session replacement do not request `max_age`, `prompt=login` or essential
`auth_time`. Neither operation makes old or unknown evidence recent. Future
or contradictory signed authentication times are rejected.

Ordinary management by a qualified human with current `security.manage`
authority accepts old or missing signed authentication time. Issuer trust changes
and effective expansion of management authority require a signed authentication
event after the final reviewed operation, bound to its original ID, intent and
full policy cut. A separate operation-purpose transaction
requests `max_age=0`, `prompt=login` and
`claims={"id_token":{"auth_time":{"essential":true}}}`. The Server saves that
purpose before redirecting; a callback cannot downgrade it. Missing or old
evidence fails operation approval without replacing the existing session or
changing its CSRF proof, expiry or policy revision. Token `iat`,
callback time, consent and account selection are never authentication-time
substitutes. Ordinary Google login and high-impact operation approval therefore
have separate acceptance records.
Bootstrap may create a mutable scoped data Role and assign it to the exact
verified identity while preserving its environment-owned `security_admin`
assignment. Bootstrap grants no data by itself. Data grants alone require no
additional authentication or extra initial provisioning. Current expiry,
admission, CSRF, CAS, environment-owned locks and administrator invariants remain.
Machines may perform authorized reference/status reads but no management mutation.

Google's [OIDC documentation](https://developers.google.com/identity/openid-connect/openid-connect)
describes `auth_time` as an optional claim that must be requested and enabled.
Its [Security bundle setup](https://developers.google.com/identity/siwg/security-bundle)
requires a published, verified OAuth app, then **Settings → Advanced Settings →
Session age claims**. These extra claims, Security bundle and Google app
publication/verification are optional outside baseline implementation, acceptance,
Issue-closure and release gates. A **Testing** client can prepare ordinary login
and need not enable these extensions for ordinary management. Google's documented
OIDC `prompt` values do not establish support for Lantern's `prompt=login`
request. Lantern's redirect therefore cannot
promise to refresh an old Google session: the returned signed evidence must
still prove an event after final review. Lantern waits until the next whole-second
NumericDate boundary before opening the challenge; same-second old evidence is
insufficient.

Google app publication/verification is a separate operator decision. If the
selected client cannot supply fresh signed evidence, record high-impact approval
as unavailable and keep that acceptance exit open; this does not disqualify an
otherwise verified ordinary login or ordinary qualified-human management.
Human API Bearer use additionally needs an exact enrolled subject and qualification
that the actual Issuer's client-credentials profile cannot collide with or
impersonate that human. Do not infer this contract from Google login alone or set
the qualification flag without verifying the actual API issuance profile.

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

   Using the reserved placeholder Admin origin, the Authorized redirect URI
   template is:

   ```text
   https://replace-admin-host.invalid/auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38
   ```

   Replace the origin privately with the exact approved HTTPS host/port before
   saving; `.invalid` is not a usable callback host. Do not
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
grants, account-linking rules or substitutes for `sub`. Subject discovery needs
an operator-owned verified Code exchange; unverified JWT decoding cannot supply
first-administrator authority.

Complete the [human configuration](../deploy/examples/auth/README.md#environment-json-startup)
privately using the [setup template](../deploy/examples/auth/security-config.json.example).
Its Google-specific fields can be:

```json
{
  "initial_admin": {
    "issuer": "https://accounts.google.com",
    "subject": "REPLACE_VERIFIED_ADMIN_SUBJECT"
  },
  "issuers": [
    {
      "issuer": "google",
      "client_id": "REPLACE_GOOGLE_WEB_CLIENT_ID",
      "client_secret": "REPLACE_RAW_GOOGLE_WEB_CLIENT_SECRET"
    }
  ]
}
```

This fragment belongs in the complete human configuration alongside `public_origin`,
explicit `target_platform`, required `material_root`, separate
`persistence_root`, fixed named nodes and the selected
public TLS/proxy boundary. The `google` preset pins the canonical Google issuer,
RS256 and token origin `https://oauth2.googleapis.com`; it derives the exact
callback and secret destination binding. Supply only the raw
`web.client_secret` value as `client_secret`, through the approved private secret
injection boundary, not the downloaded Google client JSON. Keep private copies
and generated output outside the repository; never place secret values in shell
arguments, logs, client builds or shared rendered configuration.

Fresh provisioning requires the initial administrator's enabled Issuer to
include a usable browser profile for login and operation reauthentication.
Browser-only Google login does **not** require `api_audience`. An optional
external API JWT profile needs its own explicitly qualified audience and
issuance rules; without that profile external API JWTs are rejected. Google
access tokens for Google APIs and Google ID tokens are not Lantern's RFC 9068
API Bearer profile. Browser login creates Lantern's own HttpOnly session;
native SDK/automation credentials and Role assignments remain separate.
Additional API-only and native-only Issuers do not require browser setup, but
they do not replace the initial administrator's browser profile.

Inject the complete intent as `LANTERN_SECURITY_CONFIG_JSON` with
`LANTERN_AUTH_MODE=oidc`, then use the
[provision-security command](../server/cmd/security_provisioning.go):

```sh
lantern provision-security --operator-directory /absolute/private/operator
```

The command creates independent identity keys, fixed topology and historical
trust under `material_root`; no saved setup/current Issuers, provider secrets or
TLS/proxy configuration become a runtime fallback. It opens no native state,
trusted-time owner or listener. The target remains explicit when generating Linux
material on a Mac. Preserve keys, CA/member pins and historical genesis.

Keep supplying the **same complete human JSON** on the actual target:

```sh
lantern init-security
lantern
```

`init-security` creates native state once; every normal start reads current
configuration again and resumes intact CLEAN custody. Missing/partial input,
material or state fails closed without fresh fallback or regenerated trust.
`initial_admin` is the original fresh seed and a resume identity assertion,
never a policy reset or restoration of a suspended user's assignments.

Add a later provider by updating `issuers` in that same configuration and
restarting with the updated value. Native certified compare-and-swap reconciles
configured trust before public readiness. A node with different desired Issuer
intent remains unavailable. Adding trust creates no Principals, Roles or
assignments and cannot appropriate managed trust objects or revive withdrawn
grants. No public desired revision is needed. Use the
[callback guide](../deploy/examples/auth/README.md#browser-callbacks) to derive
current callbacks from supplied origin and canonical Issuer, not persisted genesis.

Keep the generated operator and CA signing keys outside every serving mount.
Both signed control/data memberships require the separate external publisher,
even for one fixed node. Use the
[renewal workflow](live-membership-renewal.md#renew-generated-workload-certificates)
and [renew-security help](../server/cmd/security_renewal.go) for finite-window
maintenance: prepare and review same-root/key certificate candidates, drain
nodes to CLEAN, stop the publisher, install reviewed files at their original
paths, explicitly advance/resume its retained journal, then restart nodes.
This does not hot-reload certificates, renew provider credentials or rotate the
fixed root. Real provider/account configuration and acceptance remain separately
authorized work; source provisioning tests and these placeholders establish no
successful Google sign-in.

## Record actual provider results

From a clean exact candidate, verify the real callback/session flow, CSRF,
unknown/revoked users, security-admin without graph access, Role edits and
assignments, prefix Deny, scoped CDC and replica admission. Record ordinary
login with missing/old `auth_time`, unchanged recent-auth eligibility after
ordinary rotation, ordinary management without an age gate, and refusal of
high-impact approval without a provable post-review event.
Verify scoped data-Role creation and exact self-assignment, allowed in-prefix
CRUD and outside-prefix rejection without implicit grants. Retain historical
five-minute rejections as source-specific evidence. Test invalid, future and
contradictory evidence and machine restrictions separately. Test a signed
post-review event separately if the provider supports it; retain actual Google reauthentication limitations. Synthetic wire and UI contract
fixtures do not prove actual provider/browser acceptance.
The user performs the Google sign-in and any provider-owned MFA; an agent does
not collect their password or MFA code.

Public evidence records source and binary hashes, topology class, timestamps,
scenario verdicts and failure categories. Keep client configuration, tokens,
codes, cookies, account email/subject, private URLs and raw traces private.
Candidate/provider results remain separate from merged-source acceptance,
artifact publication and physical Android/iOS evidence. No merge, deployment,
Google app publication or SDK release is authorized by this runbook.
