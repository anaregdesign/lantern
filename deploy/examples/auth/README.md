# Private developer TLS, OIDC and OAuth configuration

These are **placeholder-only inputs**, not a configured environment or an
acceptance record. Replace every `REPLACE_*`, `replace_*` and `invalid.replace.*`
value in a private copy. Reserved `.invalid` hosts cannot serve as real callback
or certificate names. Even normally public client, account, Team and Key IDs,
validation endpoints and certificate paths stay outside Git.

| File | Consumer and boundary |
| --- | --- |
| [security-config.json.example](security-config.json.example) | Small, deliberately invalid setup intent for `provision-security`; used before the OIDC Compose example. |
| [server-off.env.example](server-off.env.example) | Explicit unauthenticated public OFF mode, without an IdP or authentication Store. |
| [server.env.example](server.env.example) | Advanced original file-backed Server input for externally provisioned cohorts and validation profiles. Do not mix it with environment JSON. |
| [google-issuer.change.json.example](google-issuer.change.json.example) | Existing `graph.v1.SecurityChange` ProtoJSON: Google browser and native Issuer trust, initially disabled. |
| [apple-issuer.change.json.example](apple-issuer.change.json.example) | The same API shape: separate native iOS and web-backed Android profiles, initially disabled. |
| [flutter-android.json.example](flutter-android.json.example), [flutter-ios.json.example](flutter-ios.json.example) | Flutter `--dart-define-from-file` build settings for the maintained app. No secrets. |
| [google-ios-url-types.plist.example](google-ios-url-types.plist.example) | An Info.plist dictionary fragment for the registered Google reversed client URL scheme, not a replacement Runner Info.plist. |
| [oauth-clients.env.example](oauth-clients.env.example) | Operator registration worksheet in shell-assignment format. No Lantern loader reads these names; this is not a new runtime schema or an automatic provisioning tool. |

## Environment JSON startup

Use `LANTERN_AUTH_MODE=oidc` and `LANTERN_SECURITY_CONFIG_JSON` for explicit
provisioning, then select the generated node document for initialization and
normal startup. The [Compose example](../../compose/README.md#oidc) mounts one
node's private directory and a separate writable state volume. No external
policy database is needed.

### 1. Provision trust from setup intent

Privately replace the values in [security-config.json.example](security-config.json.example).
The small setup input contains only operator choices:

- `public_origin`: exact HTTPS browser/API origin; no path, query or fragment.
- `target_platform`: explicit `linux` or `darwin`. Generating on a Mac for Linux
  containers still requires `linux`; provisioning does not sample native time.
- `persistence_root`: absolute target directory for retained node state.
- `initial_admin`: exact verified `issuer` and `subject`, never an email address.
  Its enabled Issuer must include a browser profile so the administrator can log
  in and complete operation-purpose reauthentication. API-only/native-only
  Issuers can still be configured independently.
- `nodes`: fixed one-to-31-node topology. Each entry selects `name`, exact HTTPS
  `control_origin`/`data_origin` with ports and matching
  `control_listen`/`data_listen` addresses. One entry is standalone. Live topology
  expansion and dynamic membership are not supported.
- `issuers`: intended browser, external API and/or native client profiles.
- `tls_termination`: `server` with `tls.certificate_pem`/`private_key_pem`, or
  `proxy` with the existing exact trusted-proxy/public-origin boundary. Optional
  IdP `root_ca_pem`, `private_origins` and `trusted_proxy_ips` retain their strict
  endpoint and address checks.

A browser-only Google entry can use `"issuer":"google"`, `client_id` and
`client_secret`. This preset pins the canonical Google issuer, RS256 and token
origin. Lantern derives the callback and exact secret recipient binding. A
browser profile does not require `api_audience`. Configure `api_audience` only
for an independently qualified external API JWT profile; without it, external
API JWTs are rejected. API-only profiles do not require browser client/redirect
setup. Native profiles stay in `native_clients` and retain their separate client,
audience/presenter and secret-destination checks. A provider secret cannot be
retargeted by a management change.

Register the exact browser callback with the provider. For Google's canonical
issuer, append
`/auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38`
to `public_origin`. Native callback/presenter registration stays separate.

Inject this setup JSON through an approved secret manager, then run:

```sh
lantern provision-security --output /absolute/private/provisioned \
  --operator-directory /absolute/private/operator --valid-for 720h
```

Both output directories must be new, private absolute paths. The command
creates independent TLS, voting, origin and operator keys once, derives coherent
capacity/time profiles and shared CDC keys, then writes validated trust.
It does not initialize native journals, acquire runtime time authority or start
listeners. It never overwrites retained or interrupted output. Keep failed
partial output for inspection; rerunning is not a repair procedure.

Each `nodes/<name>/security.json` is private immutable serving provisioning.
The same node directory contains its workload material and renewable signed
`membership/control.json` and `membership/data.json`. The separate operator
directory owns control/data signing keys, the private CA signer
`ca-signing-key.pem`, `publisher.json` and the retained `state.json` journal path. Never mount that operator directory into a serving node.
Generated absolute paths are final target paths: preserve them when mounting or
installing these files. Do not edit genesis, identities, profiles or paths to
make an existing deployment start.

### 2. Renew membership and initialize each target

Run the separate operator process with its generated finite-window configuration:

```sh
membershippublisher --config /absolute/private/operator/publisher.json \
  --state /absolute/private/operator/state.json --mode init
```

After an orderly publisher stop, use `--mode resume` with the same retained
journal. Both control and data manifests have short lifetimes and require
renewal even for one node. The publisher signs and durably journals each exact
candidate before publication; partial publication retains the same candidate.
Mount the node directory read-only in Server so atomic manifest replacement
remains visible. The serving process never has the signing keys.

The default window is 30 days (`720h`); `--valid-for` selects its finite horizon.
Workload leaf certificates cover the cleanup window plus one day. The separate
private CA root has a fixed ten-year life. Before the leaf/window boundary,
prepare candidates:

```sh
lantern renew-security --operator-directory /absolute/private/operator \
  --output /absolute/private/new-renewal --valid-for 720h
```

For later renewals add `--publisher-config` with the actual current immutable
publisher config. Follow the [explicit maintenance sequence](../../../docs/live-membership-renewal.md#renew-generated-workload-certificates).
The command writes only a new candidate directory; it never publishes into live
files or changes the retained journal. Review `publication.json` and require the
last-written `COMPLETE` marker before applying anything. Certificate renewal
preserves root, keys, SPKI/URI/SAN, topology and state bindings; it does not renew
public ingress TLS or provider credentials. Nodes need an orderly CLEAN stop and
restart, not hot reload or reinitialization. The root expiry is a hard boundary:
renewal cannot create a new root or reinterpret/reset retained state. Stopped or
expired membership remains fail-closed; never replace missing journal/lease state
with a fresh publisher.

On each actual target, select only that node's document:

```sh
export LANTERN_AUTH_MODE=oidc
export LANTERN_SECURITY_CONFIG_JSON='{"provisioned_file":"/absolute/private/provisioned/nodes/node-1/security.json"}'
lantern init-security
lantern
```

`init-security` creates new native control and data membership/custody state,
then publishes CLEAN only after successful orderly shutdown. It accepts no
arguments and does not regenerate trust. Native trusted-time premises still
apply on the actual Linux/Darwin target. The fixed quorum is needed for serving;
per-node initialization can occur before the other fixed members are running.

### 3. Resume intact state

Normal startup accepts only the generated reference, validates the exact target
profile/trust and reopens intact state. Missing, partial, malformed, copied,
tampered or incompatible state fails clearly; it never initializes or resets
state. Keep the retained state across stops/recreations and require successful
orderly shutdown. Initial administrator/Issuer input is an original seed, never
restart reconciliation: later current Roles, revocations, sessions and managed
Issuer changes remain authoritative.

The advanced original file-backed interface below is for deliberate external
provisioning and validation profiles; it is not a compatibility reader or an
additional requirement. Do not mix it with the generated-reference input.
There is no reader or migration for the retired complete inline-authority JSON.
Keep real setup JSON, generated documents and provider credentials out of Git,
build contexts, shell arguments, diagnostics, Admin/Vite, Flutter build defines
and shared rendered Compose output. Source tests and placeholder examples do not
qualify real providers or physical devices.

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
