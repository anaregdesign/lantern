# Private developer TLS, OIDC and OAuth configuration

These are **placeholder-only inputs**, not a configured environment or an
acceptance record. Replace every `REPLACE_*`, `replace_*` and `invalid.replace.*`
value in a private copy. Reserved `.invalid` hosts cannot serve as real callback
or certificate names. Even normally public client, account, Team and Key IDs,
validation endpoints and certificate paths stay outside Git.

| File | Consumer and boundary |
| --- | --- |
| [security-config.json.example](security-config.json.example) | Readable, deliberately invalid placeholder for the single `LANTERN_SECURITY_CONFIG_JSON` value used by the OIDC Compose example. |
| [server-off.env.example](server-off.env.example) | Explicit unauthenticated public OFF mode, without an IdP or authentication Store. |
| [server.env.example](server.env.example) | Advanced original file-backed Server environment retained for existing provisioned cohorts. Do not mix it with environment JSON. |
| [google-issuer.change.json.example](google-issuer.change.json.example) | Existing `graph.v1.SecurityChange` ProtoJSON: Google browser and native Issuer trust, initially disabled. |
| [apple-issuer.change.json.example](apple-issuer.change.json.example) | The same API shape: separate native iOS and web-backed Android profiles, initially disabled. |
| [flutter-android.json.example](flutter-android.json.example), [flutter-ios.json.example](flutter-ios.json.example) | Flutter `--dart-define-from-file` build settings for the maintained app. No secrets. |
| [google-ios-url-types.plist.example](google-ios-url-types.plist.example) | An Info.plist dictionary fragment for the registered Google reversed client URL scheme, not a replacement Runner Info.plist. |
| [oauth-clients.env.example](oauth-clients.env.example) | Operator registration worksheet in shell-assignment format. No Lantern loader reads these names; this is not a new runtime schema or an automatic provisioning tool. |

## Environment JSON startup

The standard environment-JSON interface uses `LANTERN_AUTH_MODE=oidc` plus one
`LANTERN_SECURITY_CONFIG_JSON` value containing configuration and credential
material. The [standalone Compose examples](../../compose/README.md#separate-off-and-oidc-examples)
show OFF and OIDC separately and build a matching Server from the checkout.
The old file-backed variables below are an advanced independent input path;
mixing both paths is rejected.

Supply `public_origin` once. Each browser Issuer entry contains the exact
`issuer`, `client_id`, qualified `api_audience`, explicit algorithms, approved
`token_origin` and `client_secret`. The Server derives the callback URL from
the public origin and canonical Issuer, and compiles the exact secret recipient
binding internally. A management change cannot retarget that secret. Native
client profiles belong to their Issuer entry; they do not require a separate
secret-binding environment variable. Optional TLS certificates/private keys,
IdP root CA and exact trusted proxy/private-origin settings belong to the same
JSON. Treat the complete value as a secret.

The `authority` object preserves the existing independently provisioned original
genesis, participant, membership profile, identity and limits. Its inline
`identity` uses PEM roots/certificate/TLS key and a base64 voting key;
`membership.public_key` and `origin_key` are base64 original keys. Empty objects
and `FAKE_*` strings in the template are intentional missing inputs, not valid
defaults. At initialization, public Issuer/administrator/Role metadata must
match the original genesis; a list of OAuth clients cannot manufacture authority.
During normal restart, local credential bindings may also cover a later managed
Issuer or a not-yet-registered Issuer. They do not register it, grant access or
replace current trust: ordinary authentication still requires the current
registered Issuer and exact secret recipient binding. The original
participant journal paths, membership state and `floors_file` must point into
the retained workload-owned security volume.

Only `membership.manifest_file` remains a renewable, nonsecret runtime input.
Keep its signed finite-lifetime manifest current through the existing operator
procedure, using a read-only directory mount so atomic replacement is visible.
Never mount the operator signing private key. The independent authority proof
and native-time rules in the
[current-authority contract](../../../docs/security-current-authority.md#public-oidc-composition)
still apply.

Run `init-security` once for a new state, then use normal Server startup for
every reopen. There is no fresh/resume environment variable on this input path.
Initialization rejects retained/interrupted state; normal startup requires its
intact original binding and orderly-shutdown custody. It never overwrites live
Role or Issuer changes from the initial configuration. Secret changes take
effect through a normal orderly stop and restart, not a new initialization.
The Server consumes the JSON directly from its environment without rendering
secret material into temporary configuration files.

Replace the placeholders privately and inject the completed JSON through your
cloud's secret integration or approved local credential manager. The readable
example is not a Server input file, and it does not instruct the Server to read
one. Keep real JSON out of Git, build contexts, shell arguments, diagnostics and
shared `docker compose config` output. Never pass these secrets to Admin/Vite,
Flutter build defines or public metadata. Placeholder rendering, controlled
real-wire tests and actual provider/device qualification remain separate claims.

## Keep real inputs private

From the repository root, create external private copies:

```sh
(
  umask 077
  private_dir="$HOME/.config/lantern/development"
  mkdir -p "$private_dir"
  chmod 700 "$private_dir"
  cp deploy/examples/auth/*.example "$private_dir/"
  chmod 600 "$private_dir/"*.example
)
```

Rename and edit those copies outside the repository. Keep downloaded OAuth
JSON, certificate chains, private keys, raw client secrets, Apple client-secret
JWTs, verified subjects and independently provisioned node/genesis/membership
inputs there too. On Unix use owned `0700` directories and `0600` regular files;
Server private-file checks also reject unsafe ownership, symlinks and file
substitution. Use the existing protected owner/DACL contract on Windows.
Do not print real settings or pass secrets as shell arguments, build defines,
Issue/PR text, logs or uploaded evidence.

The root `.gitignore` guards only this directory's `private/` fallback and
`*.local.*` copies, plus specific accidental provider downloads. Prefer the
external directory anyway: ignores are not access control, do not protect Git
history, and can be bypassed with `git add -f`. Deliberate synthetic fixtures
remain tracked.

## Server runtime and committed provider trust

Choose one reviewed private Server environment in a clean shell. OFF is the
Server default; use `server-off.env.example` for an explicit OFF selection or
`server.env.example` for OIDC. Do not layer their values. Retired profile,
writer/bootstrap and injected-clock settings are rejected, even when empty
or when selecting OFF. Invalid OIDC never falls back to OFF.

```sh
set -a
. "$HOME/.config/lantern/development/server.env" # Or the private server-off.env.
set +a
# Start the Server through your existing build/run procedure.
```

The public TLS certificate must cover the actual HTTPS endpoint hostname and
include the required chain. Clients must trust its issuer through their normal
trust store; never use insecure verification or an HTTP native-login endpoint.
The optional public client-CA setting enables mTLS and is not needed merely to
sign in on mobile. Private workload TLS and signed membership are separate,
mandatory for private replication in both OFF and OIDC, not this public certificate setting.
An optional OIDC root-CA file extends verified IdP trust, not mobile trust.
When using an HTTPS gateway, retain the exact trusted-proxy and public-origin
contract; do not trust arbitrary forwarded headers.

The OIDC template selects `resume`, which requires original provisioning and intact
M/P/B journals plus CLEAN custody. Only a separately authorized **new** generation
can use `fresh`; never reset existing state to make a template start. No example
supplies a signed membership, original node/genesis document, time attestation,
administrator subject, Role or credential. Obtain those independently through
the [authority provisioning/custody contract](../../../docs/security-current-authority.md#public-oidc-composition).
The unchanged template cannot establish a serving authority.

The Issuer JSON files use the existing
[`SecurityChange.put_issuer` API](../../../proto/graph/v1/security.proto),
not the internal genesis/Image JSON format or Server environment variables.
They are **individual reviewed change inputs**, not complete Apply requests.
Use the current Prepare/review/operation-reauthentication/Apply workflow, with
the original change identity and status-only reconciliation after uncertainty.
An existing environment-owned Issuer cannot be overwritten through this API.
Its initial trust belongs to independent operator provisioning; do not rewrite
retained genesis or remove environment locks. A put replaces the managed
Issuer configuration: retain every intended existing profile and binding.

Keep `enabled: false` until all private substitutions, provider registrations
and authorized trust review are complete. Google uses its Web client ID both
for the browser exchange and the native token's exact single audience;
Android/iOS client IDs form the sorted presenter allowlist. The browser callback
path is SHA-256-derived from the canonical Google Issuer; replace its origin,
not that path. `apiAudience` is a separately qualified Lantern API audience:
Google API access tokens and Google ID tokens do not become Lantern API
credentials. Human subject namespace qualification stays false. A new
native-only Google Issuer omits all browser `clientId`, `apiAudience`,
`redirectUri` and `secretRef` fields.

Secret bindings contain absolute **file references**, never secret bytes.
The Google Web file contains only the raw client secret, not downloaded JSON.
Apple's two files contain separately audience-bound, operator-generated ES256
client-secret JWTs. Each secret file is nonempty, at most 4096 bytes and has no
newline. Keep Apple's `.p8` signing key operator-private; Lantern consumes the
signed JWT, not that key. Rotation uses atomic private-file replacement before
expiry; there is no automatic signing or refresh. Public issuer/token URLs
are canonical protocol constants, not validation-specific selections.

No example installs a registration policy or grants data/management Roles.
Signup remains default-disabled; ordinary login still requires an existing
exact verified `(issuer, subject)` Principal and explicit assignments.
See [OIDC/native operations](../../../docs/oidc-operations.md#native-client-profiles)
and [Google setup](../../../docs/google-oidc-setup.md).

## OAuth client and platform registration

Use the private operator worksheet to register or inspect actual provider
resources; it is not sourced by the Server or included in an app build.

| Platform | Provider registration and matching application setting |
| --- | --- |
| Google Web | Registered Web client ID and private raw secret file; exact HTTPS browser origin and Issuer-specific callback. The backend Web client ID is the native `serverClientId`, supplied by Server metadata, not another Dart define. |
| Google Android | Actual build `applicationId`, signing-certificate SHA-1 (and SHA-256 where the provider registration requires it) and Android OAuth client ID. Allowlist that client as a presenter; never use it as the backend audience. |
| Google iOS | Actual Bundle ID, iOS OAuth client ID, and its provider-issued `REVERSED_CLIENT_ID`. Set `LANTERN_GOOGLE_IOS_CLIENT_ID` and merge the plist fragment into a private provider-enabled Runner configuration. |
| Apple native iOS | Registered App ID/Bundle identifier with Sign in with Apple enabled, Team ID and signing profile. The Bundle identifier, not a Team-prefixed identifier or App Store numeric ID, is the native audience. Select the existing `Runner/NativeLogin.entitlements` for the provider-enabled build. |
| Apple web-backed Android | Services ID associated with the primary Apple App ID, website domain and exact HTTPS `/auth/native/apple/callback/<profile-id>` return URL. Match the build's Android package. The existing plugin's `signinwithapple` callback activity/scheme is not Apple's registered HTTPS return URL. |
| Apple operator signing | Team ID, Key ID, signing-key **file** and distinct iOS/Services client-secret JWT **files**. OAuth signing stays operator-owned; the Team ID also belongs in the required private Xcode signing settings, not Dart defines. |

Do not rename the checked-in example app's canonical package/bundle IDs just
to fill a template. Register the actual build identifiers privately and replace
the placeholder worksheet/Server fields to match them. A downloaded Google
JSON/plist remains external; this app already supplies `serverClientId` and
the iOS client ID through its supported configuration path.

From `sdks/dart/example`, select the private file for the installed platform:

```sh
flutter run --dart-define-from-file="$HOME/.config/lantern/development/flutter-android.json"
# On iOS use the private flutter-ios.json and matching native registration.
```

The Android and iOS Apple profile selections differ. Both files keep native
login HTTPS-only and omit token-endpoint/insecure overrides. Client secrets,
Apple client-secret JWTs and OAuth signing keys must not enter Dart defines,
Info.plist, Gradle, the app bundle or public metadata. Registered public client
IDs and required platform signing metadata necessarily enter the appropriate
private app build, not committed settings.
Route native Begin, Apple callback and Complete to the same Server owner using
the existing affinity contract. Parsing these examples does not qualify a running
environment. `functional-validation` checks complete behavior with controlled UTC;
`configured-source-integration` checks the normal build against its configured
time source. Neither proves real-provider/platform, physical-device or publication
acceptance. Production OIDC startup still needs its own fresh time anchor and
current quorum; an old receipt cannot supply them.
