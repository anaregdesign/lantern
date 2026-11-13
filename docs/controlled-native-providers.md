# Controlled real native providers and Admin

Issue #2149 adds the explicit `controlled-real-native-providers` test profile.
It composes the ordinary production App, provider Fetcher and strict declarative
security parser with the existing compile-time loopback UDP/native-elapsed time
source. It admits Google Android/iOS, Apple Android/iOS and an independent Google
Browser administrator client. It makes no provider, signup, real-UTC, strong
operation-reauthentication, deployment or normal configured-source qualification
claim. The Google-only [component profile](controlled-google-native.md) retains
its provider/config/profile boundaries; its shared readiness helper is corrected
to use the actual Connect Health route. Normal `native_login_gate.py --serve`
remains unchanged.

This is a finite new three-member cohort with new create-only material, operator
custody, state and container names. Existing cohorts, WAL, identities, policies,
keys and servers are never read as current state or reset. A verified historical
seed identity is an explicit input; its old credential/session is not authority.
The fresh production genesis contains only that seed, the exact default Roles
and assignments, no UID, and registration OFF. It is not a precreated signup or
personal-user test account.

## Exact private inputs

The operator prepares a private schema-1 plan of kind
`controlled-real-native-providers-plan`. An unresolved source/image/reference
is refused before admission reads, Git or child dispatch. The controller defaults
to `PLAN_ONLY_NO_INPUT_NO_LOCK_NO_RUNTIME`. No clients, secrets, subject, keys,
material or completed input plan belong in this repository.

The plan contains:

- `source`: exact `head`, `tree`, whole `source_sha256`, plus immutable `proof`
  and Root `review` references. The source proof is schema1, PASS, exact aliases
  `source_head`/`source_tree`, selected CI and required normal configured-source
  verified. Root acceptance is independently APPROVE for the same three fields.
- `build`: immutable controlled server `image` and `receipt`, passive same-binary
  `description`, stock `public_roots` references. A reference is exactly an
  absolute `path` and actual raw `sha256`. Public provider trust is the normal
  distribution bundle in the Scratch image at
  `/etc/ssl/certs/ca-certificates.crt`; private workload TLS is separate.
- `seed`: `material` and independently reviewed `verification` references. The
  latter must be `APPROVE_VERIFIED_RETAINED_SEED`, bind the exact material, say
  `provider_ownership_verified=true`, `subject_source=retained-material.initial_admin`
  and retain its selected `basis_refs` plus the historical provenance. A Root-
  reviewed byte-identical private custody derivation may relocate a selected
  input while preserving original references and provenance; an old unsuitable
  file mode is not an instruction to read that old path as current authority.
  Merely decoding an email, subject or token cannot supply this predicate.
- `issuer_intent`: hash-pinned owner-custody normal JSON with `public_origin` and
  `issuers`. Only its exact reviewed Issuers are projected into fresh generated
  configs. Its old node, root, state, TLS or initial administrator fields are not
  reused. The normal strict parser revalidates the complete generated config.
- `clients`: exact `google_browser`, `google_native`, `google_android`,
  `google_ios`, `apple_android` and `apple_ios` public IDs. The three native
  Google IDs are distinct and from one selected project. Browser can use a
  different project; a native public ID does not prove Browser secret/redirect
  registration or Google extra-claim eligibility.
- `profile_ids`: the four keys `google_android`, `google_ios`, `apple_android`,
  `apple_ios`, each with a distinct reviewed ID. `android_package` pins the
  installed application's Android callback package.
- `secrets`: exactly `browser`, `apple_android`, `apple_ios`, each containing
  `ref` and `value_sha256` (SHA256 of the original UTF8 value, no trimming).
  Secret values remain exclusively in owner-custody issuer intent/private env
  files. Google native profiles carry no secret. The Browser ref follows the
  normal compiler's explicit Issuer ID or its derived handle. Apple uses explicit
  `secret_id`. No arbitrary path-backed secret or destination override is admitted.
- `public`: canonical HTTPS `origin` at approved port24443, explicit Mac LAN
  `lan_ip`, and private `certificate`/`key` refs. `network` contains fresh empty
  dedicated bridge `id`, `name`, and its Root `admission`.
- `admin`: current exact-source immutable `image` and build `receipt`. The
  receipt kind is `controlled-real-native-providers-admin-build`, status PASS,
  exact source fields, actual image, `dockerfile=admin/Dockerfile`,
  `context_source_sha256`, `build_args={COMMIT:<head>}`, and raw
  `dockerfile_sha256`, `entrypoint_sha256`, `caddyfile_sha256`, and actual built
  `/srv/index.html` raw `index_html_sha256`. These refer to
  the current maintained Admin source; a stale image label alone is insufficient.
- `edge`: immutable image and Root `admission` with status
  `APPROVE_FIXED_CADDY_EDGE_IMAGE`, actual image and `/usr/bin/caddy` binary path.
- `output`: absent private absolute directory; new hex64 `cohort`;
  integer `lifetime_seconds` 15..540; absolute `planned_stop_at`, and
  `cleanup_by` exactly20s later. No deadline renewal or retry is available.
  The separate owner-present mode below requires an explicit `owner_window`
  and exactly1800 seconds; an omitted field never lengthens a static window.

The server build receipt kind is `controlled-real-native-providers-build`, schema1,
PASS, exact source fields, immutable image, actual binary/public-roots/roles SHA,
`build_tags=[lantern_test_time]`, and the actual compile argv. Compile the test
package with the qualified symbol:

```bash
go test -c -tags=lantern_test_time -ldflags '-X github.com/anaregdesign/lantern/server/cmd.controlledNativeProviderRevision=<exact-head>' -o <private-build>/controlled-native-providers.test ./server/cmd
```

This is a build recipe, not a serving invocation. The Scratch image entrypoint is
`/controlled-native-providers.test`, no default args or inherited Env, with the
stock public CA bundle. The separately opted-in
`TestControlledNativeProviderProfileDescription` must actually emit the same
binary/public-roots/revision/Role hashes, profile, real-google-apple source,
four-profile count and registration-at-genesis false. Exit0 for an absent test
does not qualify the image. The controller repeats this passive description
from the actual selected image before provisioning.

## Explicit 30-minute owner window (#2163)

The owner-present plan may add exactly:

```json
"owner_window": {"mode":"membership-renewal","lease_seconds":600,"refresh_seconds":60}
```

This mode requires `lifetime_seconds=1800`, one original absolute
`planned_stop_at`, and `cleanup_by` exactly20 seconds later. It rejects other
long durations, malformed renewal settings and late or inconsistent endpoints.
The controller converts the endpoints to monotonic deadlines once. Setup keeps
its existing reserve and per-command limits; serving observations consume only
the remaining original interval. Phone install, capture and provider-operation
limits remain independent and unchanged. Early `release` proves only the actual
observed interval, not that 30 minutes elapsed.

An actual new-source build is required. Its passive description with
`CONTROLLED_NATIVE_PROVIDER_DESCRIBE=1` and
`CONTROLLED_NATIVE_PROVIDER_DESCRIBE_OWNER_WINDOW=1` includes the exact
`owner_window` object and `owner_window_lifetime_seconds=1800`. Static description
without the second variable retains its existing fields. An older build,
descriptor, fixed-ten-container consumer or signed phone receipt cannot be
relabeled to qualify this mode. Build and qualify artifacts against the selected
new source and review their runtime compatibility before an owner dispatch.

After fresh provisioning, the controller prepares all three serving views and
starts `TestControlledNativeProviderPublisherChild` in a separate owned container.
It uses the existing membership publisher, with genuine signed leases of at most
ten minutes and a one-minute refresh. It never increases production membership
lifetime or admits an expired lease. The publisher receives only the two operator
signing-key files, public roots and certificate copies read-only. It receives no
CA signing key, workload private TLS key or provider secret environment. Serving
containers receive none of the operator custody or signing keys.

Only the six selected `views/<index>/identity/nodes/node-<n>/membership/`
`control.json` and `data.json` files may be atomically renewed. Their parent
directories are mounted writable only in the publisher, for atomic replacement;
unexpected files, sidecar changes, identity/key/configuration changes and all
other material drift remain failures. The publisher journal has its own separate
mutable directory. Provisioned original manifests and operator configuration
remain immutable. No mutable root or broad custody mount is shared with serving.

Before an App starts, a verified publisher Open and initial Step create one
immutable public `publisher-public/capacity.json`. This record binds the exact
revision/cohort, original stop/cleanup, fixed renewal settings, public profile,
domain and operator-key digests, three targets per stream and certificate capacity.
It contains no private key or credential. Only this public file is mounted
read-only at `/owner-capacity.json` in initializers and resumed Apps. Publisher
issuance ends at original `cleanup_by+5s`, its certificate cleanup budget at
`cleanup_by+10s`; the controller still stops and joins every owned container by
the original server cleanup deadline. These extra publisher capacity bounds do
not extend serving authority. Workload certificates, public TLS and actual
provider secret validity must cover their complete admitted intervals.

Each native owner checks the actual signed control and data manifests every
second. Serving cuts use actual borrowed `CurrentAuthority` native time bounds,
with the first live witness before READY and the last before the owned clock
closes. Invalid/expired bounds or signatures, rollback, profile/generation/member
changes and renewal gaps cancel that App. This does not require full Admin
readiness to stay true during legitimate management CAS. Bounded transcripts
retain the real public signed envelopes and changed versions; terminal native
acceptance replays signatures, overlapping intervals and the contiguous native
serving block. The controller separately checks envelope bytes/hashes, stable
profile/domain, monotonic versions, native bounds and transcript hashes/counts.
Constructor/initializer and post-close teardown cuts are explicitly host-auxiliary;
they never become native UTC evidence. The App's unchanged native trusted-time
and private TLS guards remain authoritative. The controller checks publisher/App liveness and
publisher publication status as well. An unexpected publisher exit fails the
window even while the last signed ten-minute lease is still current.

The owner lifecycle has eleven containers, including the separately joined
publisher. Its descriptor carries `owner_window`, the public capacity reference
and the explicit container count. Normal cleanup requires the publisher's actual
stop acknowledgement, all native lease transcripts to cover their observed
intervals, and every owned container to stop normally. A separate initially empty
`owner-control` directory is mounted read-only in the Apps and publisher. Before
Docker stop, the controller creates and syncs its only permitted file, `stop.json`:
schema1, kind `controlled-native-provider-owner-stop`, exact revision, nonce and
original stop/cleanup endpoints. Each App reads and hashes it before its last
actual native witness and original Shutdown/Close; the publisher reads it before
Close. Their joined records must acknowledge the actual Root-written raw hash.
This causal proof uses no assumed Mac-to-VM wall-clock offset. VM timestamps
remain auxiliary within their own observed lifecycle. The control file is
create-only operational metadata, not mutable identity/custody material. A failed
stop-record write still attempts all normal container stops and is nonaccepting.
Unexpected control material, missing acknowledgements, forced/unknown cleanup or any continuity
failure is nonaccepting and retains the original shared lock when cleanup is
unknown. No automatic restart, cohort reuse, clock reset or external NTP
acquisition is introduced. Production `--serve`, the Google-only component and
the independent mandatory normal configured-source qualification are unchanged.

## Public origin and normal management journey

Only the new edge publishes the selected LAN address at24443. New node1 gets a
scoped bridge alias for the approved public hostname. Edge wire routes and the
Admin image's maintained `/auth/*`, `/browser/*`, `/graph.v1.*/*` BFF point only to
that new node1's TLS6380. Admin serves its current-source SPA. No old upstream,
backend fallback, provider hostname mapping, synthetic provider, private provider
CA or `/etc/ntp.conf` mount is used. An ordinary Docker bridge is not a claimed
DNS/HTTPS-only firewall. Actual provider HTTPS uses production pinned destinations
and public trust; time uses the compiled local UDP route.

Normal parser callback bindings remain:

- Google Browser: public origin + `oidc.CallbackPath(GoogleIssuer)`.
- Apple Android: public origin + `/auth/native/apple/callback/<exact-profile-id>`,
  then the ordinary fixed Android package deep-link flow.
- Apple iOS: native code completion, no fabricated Browser redirect.
- Exactly three secret tuples: Google Browser at `https://oauth2.googleapis.com`,
  and the two separate Apple client bindings at `https://appleid.apple.com`.

The owner must use the normal Admin page at that origin, log in legitimately,
read current typed Roles/policy, Review exactly the four Native registration
profiles with only `personal_user`, perform the exact per-operation reauth,
Save, and obtain fresh typed readback. The controller sends no management RPC,
Apply, RegisterUser, Role or UID mutation. Google callback/auth_time failure is
still an acceptance blocker if observed; a fresh private Browser context,
account chooser, JWT iat or synthetic seed is not an operation freshness proof.
Blocked Browser origins require owner manual interaction, never an API bypass.

Only after actual policy readback may the reserved fresh external Gmail account
perform genuine signup. Never precreate it to substitute for signup acceptance.
Provider cells additionally require actual personal-user identity/rights,
own-resource CRUD and refusal/cross-account assertions from their maintained
matrix. Setup, a successful seed login, or a restored saved session counts none
of those assertions.

## One reviewed finite window

A separate hash-pinned owner-present dispatch has kind
`controlled-real-native-providers-owner-dispatch`, status APPROVE, the exact
`plan_sha256`, selected `seed_verification_sha256`, `approved_public_origin`,
and positive `secret_and_tls_whole_window_verified` and
`admin_and_edge_source_reviewed`. Actual Apple JWT/secret destinations, callback
registration and every signed membership/cert interval must cover setup, serving
and cleanup before this can be produced; no signing is performed here.

```bash
python3 -B .github/scripts/controlled_native_provider_fixture.py --execute-reviewed-owner-window --plan <private-plan.json> --plan-sha256 <actual-plan-SHA> --root-review <private-dispatch.json> --root-review-sha256 <actual-review-SHA>
```

The original shared exclusive host lock spans this single window and normal joins.
Three initializers return genuine terminal counters; three resumed Apps use their
original native owners, actual readiness and fixed stop. Their final typed/redacted
decision captures retain explicit dropped counts. Only normal joined lifecycles,
complete capture and unchanged source/private immutable inputs can pass. Unknown
create, stop or publication outcomes retain the original lock in a normally
terminable idle holder for explicit Root resource resolution. There is no rm,
uninstall, repeat login/code exchange, second window, reset or automatic recovery.

Signal-handler restoration is guarded in both controller finalization paths
(#2164). A restoration failure still attempts the remaining handler, records
nonacceptance and reaches original-lock retention. If the outer restoration fails
after lifecycle publication, a create-only nonaccepting supplement is attempted;
publication failure cannot release the original FD. This is failure-path handling,
not evidence that such a failure occurred in an actual serving window.

Before READY, one bounded isolated Python child resolves the public hostname
to exactly the approved LAN IPv4 and makes only an auth-exempt Connect Health
POST and an Admin `/index.html` GET through the actual public TLS origin. Both
use the exact admitted stock public CA file, with proxies and redirects disabled.
Health must report SERVING; index bytes must hash to the actual Admin build
receipt. DNS or TLS/content mismatch rejects readiness. The parent owns the
child's finite15s command budget, including DNS; no provider/management request,
cookie, secret or private TLS key enters its argv or output.

`STARTED.json`, `fixture.json`, exact private `ready`, create-attempt records,
bounded command logs and terminal `lifecycle.json` remain separate. A private
`release` file containing exactly `release\n` requests early normal cleanup.
`NON_ACCEPTING_POST_PUBLICATION.json` or any failure keeps the evidence
nonaccepting. The descriptor always records provider components0, full cells0,
signup0, no real UTC/normal qualification or Admin strong-reauth claim. Actual
device/provider/registration evidence is a separate owner-directed acceptance.
