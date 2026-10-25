# Private developer TLS, OIDC and OAuth configuration

These are **placeholder-only inputs**, not a configured environment or an
acceptance record. Replace every `REPLACE_*`, `replace_*` and `invalid.replace.*`
value in a private copy. Reserved `.invalid` hosts cannot serve as real callback
or certificate names. Even normally public client, account, Team and Key IDs,
validation endpoints and certificate paths stay outside Git.

| File | Consumer and boundary |
| --- | --- |
| [security-config.json.example](security-config.json.example) | One human JSON used unchanged by provision/init/start in the OIDC example; placeholders are deliberately invalid. |
| [server-off.env.example](server-off.env.example) | Explicit unauthenticated public OFF mode, without an IdP or authentication Store. |
| [server.env.example](server.env.example) | Shell wrapper for the same human JSON configuration; no alternate file-backed runtime schema. |
| [google-issuer.change.json.example](google-issuer.change.json.example) | Existing `graph.v1.SecurityChange` ProtoJSON: Google browser and native Issuer trust, initially disabled. |
| [apple-issuer.change.json.example](apple-issuer.change.json.example) | The same API shape: separate native iOS and web-backed Android profiles, initially disabled. |
| [flutter-android.json.example](flutter-android.json.example), [flutter-ios.json.example](flutter-ios.json.example) | Flutter `--dart-define-from-file` build settings for the maintained app. No secrets. |
| [google-ios-url-types.plist.example](google-ios-url-types.plist.example) | An Info.plist dictionary fragment for the registered Google reversed client URL scheme, not a replacement Runner Info.plist. |
| [oauth-clients.env.example](oauth-clients.env.example) | Operator registration worksheet in shell-assignment format. No Lantern loader reads these names; this is not a new runtime schema or an automatic provisioning tool. |

## Environment JSON startup

Supply `LANTERN_AUTH_MODE=oidc` and the same complete human-authored
`LANTERN_SECURITY_CONFIG_JSON` to provisioning, initialization and every start.
No command replaces this input with a generated runtime configuration. The
[Compose example](../../compose/README.md#oidc) forwards it to both init and
serving while mounting identity material read-only and native state separately.

### Configuration

Complete [security-config.json.example](security-config.json.example) privately:

- `public_origin`: exact HTTPS public origin, without a path/query/fragment.
- `material_root`: required clean absolute directory for retained identity keys,
  fixed trust and renewable membership inputs. It must not overlap state or
  operator custody. It is not a saved user configuration.
- `persistence_root`: separate absolute target root for retained native state.
- `target_platform`: explicit `linux` or `darwin`. Provisioning on a Mac for Linux
  containers still selects `linux` and does not acquire runtime time authority.
- `nodes`: fixed one-to-31-node topology, each with `name`, exact HTTPS
  `control_origin`/`data_origin` ports and matching
  `control_listen`/`data_listen` addresses. `node` selects the local named entry;
  it can be omitted only when the list has exactly one entry.
- `initial_admin`: exact verified `issuer` and `subject`. Fresh provisioning
  requires an enabled browser profile for its login and operation reauthentication.
  On resume this is only the original identity assertion, never a regrant.
- `issuers`: current desired configuration-owned provider trust, with browser,
  external API and/or native profiles and their private client secrets.
- `tls_termination`: `server` with `tls.certificate_pem`/`private_key_pem`, or
  `proxy` with exact `trusted_proxy_ips`. Optional IdP `root_ca_pem` and
  `private_origins` preserve strict TLS and endpoint/SSRF checks.

A browser-only Google entry can use `"issuer":"google"`, `client_id` and
`client_secret`. The preset pins canonical issuer, RS256 and token origin, and
derives the callback/secret recipient. It needs no `api_audience`; without a
separate API profile external API JWTs are rejected. API-only profiles need no
browser client/redirect. Native profiles retain distinct audiences/presenters and
secret destinations. Provider credentials, public TLS and proxy settings must be
supplied again on every start; they are never loaded from identity material.

### Provision once, initialize once, then resume

Inject the completed JSON through an approved secret manager. With that same
value still supplied, run:

```sh
lantern provision-security --operator-directory /absolute/private/operator --valid-for 720h
```

`material_root` in the JSON selects the new material directory; there is no
second output-directory option. Both material and operator directories must be
new private absolute paths. The command generates independent TLS, voting,
origin, operator and CA keys plus coherent fixed profiles once. It opens no
native state, trusted clock or listeners. Existing/partial output is never
rewritten or repaired by rerunning.

`material_root/nodes/<node>/identity.json` retains independent keys, historical
trust anchors, fixed topology and original seed identity. It contains no saved
setup/current Issuer list, provider secrets, public TLS or proxy configuration.
Historical genesis may retain original trust records as anchors; those records
are never a current provider-config source. Durable CDC keys and validated finite
internal capacities stay bound to that identity. Retained state under
`persistence_root/<node>` holds native security history and custody. Neither directory can replace missing current input. Preserve paths,
keys, historical genesis and every existing state/lease family.

The separate operator directory contains control/data signing keys, private
`ca-signing-key.pem`, `publisher.json` and the retained `state.json` journal path.
Start its external publisher before the short initial manifests expire:

```sh
membershippublisher --config /absolute/private/operator/publisher.json \
  --state /absolute/private/operator/state.json --mode init
```

It renews both control and data memberships, even for one node. After an orderly
publisher stop use `--mode resume` with the same journal. Mount only the selected
node's material directory read-only in Server; a directory mount preserves
atomic renewable input replacement. Never mount operator/CA signing custody.

On each actual target, keep supplying the same human JSON and run:

```sh
lantern init-security
lantern
```

`init-security` explicitly creates native control/data membership and custody
state once and publishes CLEAN only after orderly shutdown. It accepts no
arguments. Ordinary startup always resumes intact state and uses current JSON
again. Missing JSON, material, partial/altered/copied state or trust/target
mismatch fails closed; it never falls back to a stored configuration, fresh
initialization or regenerated trust. Preserve successful CLEAN shutdown on every
normal stop/restart. Fixed membership is not live standalone-to-cluster expansion.

### Change configured Issuers later

Add a later provider to `issuers` in the same configuration, keep all intended
configuration-owned Issuers, and supply that updated value on restart. Native
certified compare-and-swap reconciles configured trust before public readiness.
Nodes with different desired Issuer intent remain publicly unavailable.
Unchanged intent does not rewrite policy. No public version or manual desired
revision is needed.

Adding trust creates no Principals, Roles or assignments. Reconciliation cannot
appropriate managed Issuers, reset application/security state, undo account
suspension or revive withdrawn permissions. Existing admin and last-usable-human
invariants still apply. `initial_admin` remains the exact original seed identity;
changing or repeating it cannot reset later administrator state. Current
provider secrets are bound to their exact issuer/client/token destination.

Management-owned Issuer worksheets can reference optional current secret
bindings supplied in this same JSON. For the example worksheet handles, the
shape is:

```json
{
  "secret_bindings": {
    "replace_google_web": {
      "issuer": "https://accounts.google.com",
      "client_id": "REPLACE_GOOGLE_WEB_CLIENT_ID",
      "token_origin": "https://oauth2.googleapis.com",
      "value": "REPLACE_RAW_GOOGLE_WEB_SECRET"
    },
    "replace_apple_android": {
      "issuer": "https://appleid.apple.com",
      "client_id": "invalid.replace.apple.web",
      "token_origin": "https://appleid.apple.com",
      "value": "REPLACE_APPLE_ANDROID_CLIENT_SECRET_JWT"
    },
    "replace_apple_ios": {
      "issuer": "https://appleid.apple.com",
      "client_id": "invalid.replace.ios",
      "token_origin": "https://appleid.apple.com",
      "value": "REPLACE_APPLE_IOS_CLIENT_SECRET_JWT"
    }
  }
}
```

This is a fragment to merge into current operator input, never a second startup
document or a trust grant. A handle must match its Issuer/native client's
`secretRef`; issuer, client ID and token endpoint **origin** must all match the
verified destination. Do not duplicate a handle generated by a configured
profile's inline secret. The `*_FILE` worksheet values below are operator-only
registration/rotation notes; this example supplies raw secret values through
the protected JSON, never through those retired startup environment recipes.

### Browser callbacks

Register the exact callback derived from supplied `public_origin` and canonical
Issuer. For Google append
`/auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38`.
This local command prints only browser callback URLs from the supplied JSON:

```sh
python3 - <<'CALLBACKS'
import hashlib, json, os
config = json.loads(os.environ["LANTERN_SECURITY_CONFIG_JSON"])
for item in config["issuers"]:
    if item.get("client_id"):
        issuer = "https://accounts.google.com" if item["issuer"] == "google" else item["issuer"]
        print(config["public_origin"] + "/auth/callback/" + hashlib.sha256(issuer.encode()).hexdigest())
CALLBACKS
```

Do not print the full JSON or inspect persisted genesis to recover current
provider configuration. Native callback/presenter registrations remain separate.

### Certificate maintenance

The default operator window is 30 days (`720h`). Private workload leaves cover
the cleanup window plus one day; the private CA root has a fixed ten-year life.
Before the leaf/window boundary prepare same-identity candidates:

```sh
lantern renew-security --operator-directory /absolute/private/operator \
  --output /absolute/private/new-renewal --valid-for 720h
```

Later renewals add `--publisher-config` with the current immutable publisher
config. [Reviewed maintenance](../../../docs/live-membership-renewal.md#renew-generated-workload-certificates)
uses CLEAN node stop, stopped publisher, reviewed certificate/material install
at original paths, explicit journal advance/resume, then restart with the same
human configuration. Preparation reads retained identity/operator custody, never
saved setup or provider credentials. It does not hot-reload, rotate root/keys,
reset state or renew public ingress/provider credentials. Root expiry remains a
hard boundary requiring a separately specified trust transition.

There are no parallel legacy file-backed OIDC environment recipes or generated
configuration references. Externally provisioned material uses this same typed
configuration and material format. OFF works without JSON, but any present
security JSON, including empty, is rejected. Keep real configuration/credentials
outside Git, build contexts, shell arguments, logs, client builds and shared
rendered Compose output. Synthetic source evidence is not real-provider/device
qualification.

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

The OIDC template supplies the same current JSON on every invocation. Separate
identity provisioning and native initialization are explicit first-use actions;
ordinary startup requires retained identity material and CLEAN native custody.
Nothing in these placeholders supplies actual provider/account acceptance.

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

The human JSON carries exact destination-bound `client_secret` values through
the private injection boundary. Google uses the raw client secret, not downloaded
JSON. Apple's profiles carry separately audience-bound operator-generated ES256
client-secret JWTs. Secret values are bounded, nonempty and have no newline.
Keep Apple's `.p8` signing key operator-private; Lantern consumes the signed JWT,
not that key. Rotation supplies updated current JSON on restart before expiry;
there is no automatic signing or refresh. Public issuer/token URLs are canonical
protocol constants, not validation-specific selections.

Fresh configuration seeds only the explicit initial administrator's management
assignment; it grants no application data access. No template installs a
registration policy. Signup remains default-disabled; ordinary login requires an existing
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
