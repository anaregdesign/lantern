# Controlled-time real Google component preparation

[#2138](https://github.com/anaregdesign/lantern/issues/2138) adds the explicit
`controlled-real-google-component` test profile. It serves the production App,
OIDC Fetcher, TLS, native authority, quorum, WAL and expiry guards against genuine
Google discovery/JWKS. Only time acquisition is the compile-time
`lantern_test_time` loopback UDP/native-elapsed fixture. Its disclosed host seed
interval is synthetic correlation, not proof of real UTC or normal
configured-source qualification. Production builds and the existing normal and
synthetic fixture paths are unchanged.

This profile is preparation for **one interactive existing-account Google
SignIn/session observation per Android/iPhone**. Setup cannot pass either
component. It cannot qualify the four full provider cases, self-registration,
UID allocation, CRUD, Apple or Admin operation freshness. An automatically
restored Lantern session is a separate observation; it is not a fresh Google
SignIn. A selected single normal UI Sign out after an observed authenticated
account is local teardown only. There is no lightweight-login fallback, second
SignIn, Register or probe-capture Run in this scope.

## Inputs and exact source

The operator prepares a private schema-1 `controlled-real-google-plan` accepted
by `.github/scripts/controlled_google_fixture.py`. Calling it without
`--execute-reviewed-owner-window` is `PLAN_ONLY_NO_INPUT_NO_LOCK_NO_RUNTIME`.
Source HEAD/tree/whole-source SHA and every `{path,sha256}` reference must be
resolved before any runtime admission. A missing proof is a setup blocker.

- `source`: `head`, `tree`, `source_sha256`, `proof` and `review`. The schema-1
  main proof must actually have `selected_ci_verified` and
  `required_configured_source_verified` true, matching `source_head`/`source_tree`
  aliases; Root acceptance binds the same source. Historical qualifications
  cannot be renamed. Mandatory PR/main, pre-tag and selected-environment normal
  qualifications remain separate.
- `build`: immutable `image` ID and references `receipt`, `description`,
  `public_roots`. Build from this exact clean source using the pinned Go
  toolchain, `GOOS=linux`, selected `GOARCH`, `CGO_ENABLED=0`, empty `GOFLAGS` and
  `GOTOOLCHAIN=local`. Use the actual qualified **test-package** symbol:

  ```text
  go test -c -tags=lantern_test_time -ldflags "-X github.com/anaregdesign/lantern/server/cmd.controlledGoogleRevision=<HEAD>" -o <private-build>/controlled-google.test ./server/cmd
  ```

  The create-only Scratch context contains that one executable at
  `/controlled-google.test`, an ordinary stock public CA bundle at
  `/etc/ssl/certs/ca-certificates.crt`, and
  `ENTRYPOINT ["/controlled-google.test"]`. Record the actual command, selected
  environment, source before/after, raw logs, binary SHA, public-root SHA and
  immutable image ID. The schema-1 `controlled-real-google-build` receipt has
  `status=PASS`, the three source fields, `image`, `binary_sha256`,
  `public_roots_sha256`, `build_tags=["lantern_test_time"]`, `compile_argv` and
  `roles_sha256`. The latter comes from the actual passive description, not an
  arbitrary caller-selected Role digest.

  Run `-test.run=^TestControlledGoogleProfileDescription$` in that same image
  with only `CONTROLLED_GOOGLE_DESCRIBE=1`, network none and no authority mounts.
  Retain its single JSON event as the `description` reference. It binds revision,
  build tag, exact production default Role digest, actual executable and actual
  system public-root bytes. Exit 0 alone is insufficient: a nonexistent test
  selector also exits 0. The launcher repeats this passive event comparison
  before provisioning. Private workload/backend CA material cannot replace the
  Google public-root bundle.
- `seed`: immutable retained `material` and a Root `verification` reference.
  The exact `initial_admin` issuer/subject is read from previously verified
  material; never derive it from email, token decoding or an unverified UI label.
  Verification must state `APPROVE_VERIFIED_RETAINED_SEED`,
  `provider_ownership_verified=true`, exact `material_sha256`,
  `subject_source=retained-material.initial_admin`, and hash-bound `basis_refs`
  for the legitimate prior ownership proof. A historical declared seed hash
  alone is insufficient. Subjects, keys and tokens stay private.
- `clients`: exact operator-reviewed `web`, `android`, `ios` Google client IDs
  from one project; `profile_ids` selects two distinct native profiles. Native
  ID tokens use Web audience plus their exact platform presenter. A structural
  Browser Web client without a secret is **unused**: no Browser callback,
  redirect registration, Admin validation or shared-project change is implied.
- `public`: canonical HTTPS `origin` on the distinct approved port 24445,
  explicit approved private `lan_ip`, and private `certificate`/`key` references.
  Node 1 publishes only `<lan_ip>:24445:6380`; all-interface publishing is not
  admitted. Rebuild the signed normal-main apps for this exact source/endpoint
  through their independently reviewed build admission before account actions.
- `network`: immutable dedicated bridge `id`, `name` and Root admission
  `APPROVE_EMPTY_DEDICATED_BRIDGE` bound to that identity and cohort. The actual
  bridge must still be empty. It is **not** a DNS/HTTPS firewall: evidence states
  only compiled loopback time and the unchanged production Fetcher's fixed
  discovery/JWKS destinations/public-address/TLS checks. No Google host alias,
  synthetic provider, provider CA override, credential mint or `/etc/ntp.conf`
  mount is supplied.
- `output` is a new canonical private path and `cohort` a new 64-hex nonce.
  `lifetime_seconds` is 15..540; `planned_stop_at` is fixed and `cleanup_by`
  exactly 20 seconds later. Root's distinct `controlled-google-owner-dispatch`
  review binds the plan SHA, seed-verification SHA and actual owner presence.

The launcher calls ordinary create-only `provision-security`. Production default
genesis contains `security_admin`, `data_admin`, `personal_user`, with the seed's
exact EnvOwned security-admin and ordinary data-admin assignments, no UID and
self-registration OFF. No historical keys, native state, journal, assignments or
policy from CLEAN6 are mounted or changed. Operator signing custody is mounted
only for provisioning; each initializer/App receives only its node's identity
view and separate mutable state. It never receives the master or operator parent.

## Lifecycle, time and bounded cleanup

All actual serving remains owner-held until a separately reviewed dispatch.
Use the shared owned 0600 `/private/tmp/lantern-heavy-local.lock` with exclusive
nonblocking flock through normal joins. Do not start while another heavy gate
owns it. Resolve all inputs and fresh readiness first; do not infer readiness
from old device receipts. The exact private invocation is:

```text
python3 -B .github/scripts/controlled_google_fixture.py --execute-reviewed-owner-window --plan <plan.json> --plan-sha256 <actual-SHA> --root-review <dispatch.json> --root-review-sha256 <actual-SHA>
```

The eight owned containers are one passive description, one provisioning, three
initializers and three serving Apps. Initializers call the same production
`InitializeSecurity`, recording its actual post-Shutdown counters. Serving uses
the real Wire App, own TLS `/readyz`, process-owned settled constructor counters,
same-PID constructed/ready/joined events and stable terminal/repeated counters.
Every event binds the compiled revision, node, config digest and original signed
cohort profile. The existing bounded typed/redacted decision capture is drained
on normal and failing serving callbacks, with an explicit dropped count; no raw
provider/token/subject diagnostic is added.

Signed CONTROL/data membership has its actual **10-minute maximum**, not a
15-minute assumption. Before each actual owner, the child checks the signed
manifest signatures and actual issue/expiry windows, CONTROL identity roots and
certificate, data CA/certificate, and public listener chain against the full
remaining declared interval plus margin. The provisioner's actual signed bounds
are authoritative. No blind retry, waiting past the absolute horizon, automatic
renewal, replacement or second window is allowed.

Fixed wall endpoints are converted once to monotonic deadlines. Every setup
command must leave at least 15 seconds before the fixed stop; all cleanup commands
share the same fixed 20-second cleanup endpoint. Wall changes cannot extend it.
The driver records each create attempt before dispatch, normal-stops all known
owned IDs once, inspects terminal states and captures final App logs. A timed-out
create without returned ID remains UNKNOWN, even if other IDs stopped. No
automatic remove, alternate kill route or cleanup retry is used. Insufficient
cleanup/observation budget is nonaccepting and retains the original flock in an
idle holder until Root resolves the exact owned resources and terminates it.

Git HEAD/tree/clean/whole-source bytes, all admitted immutable references and
generated config/identity/custody views are fenced again after cleanup. Mutable
native state is retained separately. FAILs, logs, original create attempts and
partial state are preserved. Final publication and signal/deadline fences can
only turn an otherwise successful lifecycle into nonacceptance.

## Evidence and phone handoff

The new private output contains `STARTED.json`, per-command logs, create-attempt
records and `immutable-before.json`. Only all three actual Apps becoming ready
creates `fixture.json` (schema 1, kind
`controlled-real-google-component-serving`) and the exact private `ready` marker.
This descriptor explicitly says real Google, controlled time, test-only build,
no real-UTC/normal-qualification claim and **zero** component/full-case/signup
passes. Legacy synthetic/normal fixture consumers must reject this kind; do not
feed it to a release phone-record capture or invent synthetic native accounts.

The phone admission must bind this actual `STARTED.json`, descriptor/ready bytes,
current Root dispatch, exact signed normal-main build/custody, current driver
ownership, remaining fixed interval, and absence of terminal `lifecycle.json`.
Actual one update/install and conditional normal launch happen **after READY**,
then their actual records are admitted before the owner's single Google action.
They are not pre-start prerequisites. Endpoint-keyed saved Lantern credentials
may produce an ordinary startup session check; record that separately. A new
Google attempt is counted only from the planned interactive action and observed
authenticated identity/session, with the actual account/UID/rights limits stated.

Writing the private new `release` file containing exactly `release\n` requests
early normal stop; the fixed endpoint stops it otherwise. `lifecycle.json`
records actual cleanup and immutable fences. `status=PASS` there proves only
this fixture lifecycle. A `NON_ACCEPTING_POST_PUBLICATION.json`, nonzero driver
exit, UNKNOWN ownership, missing normal joins or changed inputs rejects it.
Retain all private originals; public checkpoints contain no seed or credentials.

## Focused source checks

CI runs the explicit pure tagged group with the full linker symbol and
`CONTROLLED_GOOGLE_EXPECTED_REVISION=$GITHUB_SHA`; it excludes the opt-in native
child and passive image description. Python tests use UNIT-labeled injected
clocks/processes and temporary files only. These checks cannot acquire native
time, call Google, start Docker or qualify a phone. They complement the unchanged
full gate; they do not add another source-acquisition campaign.
