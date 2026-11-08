# User registration and personal authorization

Current contract for [#1758](https://github.com/anaregdesign/lantern/issues/1758), updated by [#2076](https://github.com/anaregdesign/lantern/issues/2076). Registration policy version 2 adds explicit browser profiles and positive read/write rules. The r1/r2 prefix decisions below are retained as history. Deployment registration remains disabled until explicitly configured.

## Outcome

Register users through an explicit native Google/Apple signup operation or a permitted browser's first successful Code/PKCE login. Both use the same durable Principal, UID and initial Role operation and provide one personal area for editable metadata and general-purpose application data.

The design was reviewed against source `fd531f9efe980eaceea3b3bce0d70560d30256e3` on 2026-10-14 and uses **one shared `personal_user` Role whose typed personal-area selectors bind to a trusted immutable Principal UID**. It participates in the existing PLS semantics; there is no independent ownership bypass or mutable-record ACL. Pure Role/Access/Scope evaluation moves to `internal/authz`; durable state, consensus, credential evidence, current admission, revocation and output fencing remain with the existing single `security` owner.

**Current prefix decision: revision r2 (2026-10-14), accepted after the owner's requested five-use-case comparison. Use one personal root `users:<uid>:`. Metadata and general application data may be organized semantically beneath it; they have the same personal permissions and do not need separate Server roots. The original two-root layout is retained only in the history below.**


**Selector revision r2.1 is accepted:** use one typed `PrincipalUIDPrefix` with fixed `literal_before` and `literal_after`, inserting only the installed Principal's trusted UID. This replaces the proposed `PersonalArea.DATA` enum as the rule representation. It is neither a users-specific evaluator shortcut nor an arbitrary template language. Initial registration grants and the returned personal prefix remain only `users:<uid>:`; public grants and anonymous access are not added.

## Identity, registration and namespaces

- Add optional trusted `Principal.UserUID`: a Server-generated nonzero random 128-bit value, rendered as exactly 32 lowercase hex digits. Callers and ordinary `PutPrincipal` cannot choose or change it. Validate both exact Identity-to-Principal and UID-to-Identity uniqueness, including deleted tombstones. Machine Principals cannot carry a UID.
- Bind identity to the existing verified exact OIDC `(issuer, subject)`. Native signup uses its existing provider completion; browser signup requires an actual consumed ordinary Code/PKCE login transaction. No email linking, arbitrary claims, API token or machine credential as signup evidence. Preserve the actual Google nonce/azp profile and original signed authentication-time semantics.
- Unknown Identity: atomically allocate UID, Active Principal, first shared Role assignment and verified human-enrollment fact. Active legacy Principal without UID: explicit native signup adds UID/assignment without overwriting existing grants. Browser login for an existing Principal leaves its UID and assignments unchanged. Active Principal with UID: return the same UID and **never restore a withdrawn assignment**. Suspended/deleted: refuse signup and session issuance; do not resurrect or allocate a replacement UID.
- Concurrent registration uses the existing sys CAS/consensus. A losing prepared operation retains its original ID/intent and terminal rejection; only after that rejection is confirmed can a new Prepare use the installed UID. Reject UID collisions atomically rather than relying solely on probability.

| Area | Public logical prefix | Application examples |
| --- | --- | --- |
| Personal data, including editable metadata | `users:<uid>:` | `profile:`, `settings:`, `notes:`, `drafts:` or arbitrary subkeys; none is mandatory/reserved |

The self DTO returns UID and one `personalPrefix`. The Server imposes no application schema or suffix. The physical form is `data:users:<uid>:...`.

The trailing colon is part of the boundary; no path/case/Unicode normalization or arbitrary template expansion. Internal physical keys add `data:` through the existing mapper; SDKs must not add it. Public literal `data:`/`sys:` keys are not aliases for internal sys state. Registration creates no directory/profile Vertex and no public profile/listing. Metadata fields named `uid` or `roles` remain ordinary user data, never authority. Trusted self information comes from session/current-user reads; an initial profile Put is a separate ordinary data operation.

## Prefix decision history and use cases

Revision r2.1 supersedes the unimplemented r2 `PersonalArea.DATA` enum with the structured single-UID-slot selector; the personal root, shared Role and seven actions stay unchanged.

Revision r1 proposed `meta:users:<uid>:` plus `users:<uid>:`, two PersonalArea values, fourteen rules and two returned prefixes. It was never implemented or activated. The owner requested use-case comparisons; revision r2 supersedes that layout with one root, one selector, seven rules and one returned prefix. Both revisions use one shared Role; this revision removes duplicated selectors/ranges, not per-user Roles. Preserve r1 as history, not as an alternate live contract or a request to rename deployed data.

| Use case | r2 decision or future boundary |
| --- | --- |
| Private profile/settings/notes/drafts | Store application-defined subkeys under `users:<uid>:`; naming something `profile:` does not publish it. |
| Public profile, self-write/others-read | Future separate public-only projection with explicit static read and the same UID-prefix type for self write. These public grants are not created by registration; never grant all of `users:` to publish profiles. |
| Public/limited social posts | Keep stable personal source keys and separately authorized projections if later required. Changing a `visibility` value does not change PLS. No dynamic recipient ACL in this Issue. |
| Collaborative documents | Future stable workspace scope and trusted membership, not another user's personal root. Large dynamic groups require a separate scale/policy design. |
| Billing/Role authority | Keep authoritative records in trusted sys or service-controlled state. User-writable `billing:`/`roles:` values cannot become authority. No new billing/read-only API here. |

Type-before-UID layouts can help cross-user type queries when permissions/operations actually differ, but add disjoint personal ranges as types grow. UID-before-purpose keeps personal query/CDC/enumeration within one scope and leaves schema to applications. Literal PLS cannot express `users:*:profile:`. Public/private names carry no authority themselves; authenticated read grants also do not provide anonymous OIDC-mode access.

There is no atomic rename/move RPC; cross-prefix copy/delete is a new key/intent and does not automatically migrate Edges, receipts, CDC history or offline outboxes. Suspension and physical data deletion remain separate, and one-root deletion is not complete account erasure. Export follows the same data/read scope and configured Edge endpoint checks. Public/shared functionality is illustrative future scope, not an added completion requirement.

## Shared policy and bound authorization

The typed `PrincipalUIDPrefix { literal_before, literal_after }` data selector is mutually exclusive with a literal prefix. Management resources have no data selector. Bind `literal_before + installedPrincipal.UserUID + literal_after`. Require a nonempty UTF-8 `literal_before` ending in `:` and a nonempty UTF-8 `literal_after` beginning with `:`. The UID is canonical 32-digit lowercase hex from trusted installed state, never request/plugin/metadata input. No UID means binding rejection; machine assignment is invalid. Literal text stays byte-literal, with no normalization, recursive expansion, glob, expressions, arbitrary variable/claim paths or multiple slots. Existing literal `users:<uid>:` remains a literal string, never retroactively expanded.

Enforce the configured prefix limit (hard maximum 1,024 bytes) on `before + 32 UID bytes + after`, and the 1 MiB whole-policy bound on equivalent expanded bytes. Include both literals and UID width before marshaling/cache-key allocation; retain rule/assignment/image/capsule/cache bounds. Unknown/competing variants and global-action use fail closed. Role administrators choose the fixed literals; ordinary data callers choose neither template nor evaluated UID.

The initial shared Role has two positive rules, `data/read` and `data/write`, each with `{literal_before:"users:", literal_after:":"}`. Read includes reads, query, CDC, export and receipt disclosure within the authorized scope; write includes vertex and edge mutations subject to the configured endpoint policy. Its initial-assignment validator rejects arbitrary UID-bound roots/suffixes and other resources or permissions. An operator may reduce the Role to a safe subset. It grants no operations, schema, security, settings or replication capability.

Access is the union of assigned positive rules. `Allows`, `Scope`, `AllowsAll`, Explain, non-expansion checks and blind-response decisions use the same bound selector semantics. Existing explicit operator data grants compose normally; signup does not revoke them. Active policies reject retired Deny/effect/action fields. Earlier policy models are unsupported and fail closed.

Keep current TTL/Delete checks, refused-batch atomicity, pre-top-k/query filtering, configured Edge endpoint policies, HA Create fail-closed behavior, CDC scope and receipt original-resource/action/disclosure checks. Do not create UID-specific ranking statistics or weaken unknown-receipt full-scope proof requirements.

Compile Role structure once and bind immutable lightweight Access views using trusted `SubjectBindings{PersonalUID}`. Include the canonical binding digest in Access reuse, Scope and Edge candidate cache keys, admission scope binding and cursor/CDC authority. Prevent A/B leakage through the current Role-set-only cache and previous Snapshot reuse. Retain bounded derived caches (256 entries / 8 MiB); do not create unbounded UID maps or per-user copies of persisted Role rules. Only installed security state constructs production bindings; service request UIDs never do so. Keep `authz` standard-library-only and free of I/O, clock and Principal/session state.

## Package ownership and dependency direction

| Package | Responsibility |
| --- | --- |
| `internal/authz` | Pure typed resources and read/write rules, compile/bind, Access/Scope/Edges, bounded derived caches and Explain; pure UID syntax/prefix derivation. Standard library only; no state, I/O or clocks. |
| `internal/security` | Principal/UID uniqueness, stored Role/assignments and RegistrationPolicy, durable sys ledger/consensus, restricted registration authority, current admission/revocation/output/custody. One durable owner. |
| `internal/oidc` | Provider verification, purpose-bound transactions and opaque completions, Apple's one-time exchange. No UID allocation or grants. |
| `internal/authn` | Registration workflow and bounded volatile proofs, Prepare/Apply/status, trusted producer composition and transition into existing session issuance. |
| `service` | Strict Connect DTO/oneof/headers, affinity, limits/redaction and current output adapter. Existing data action/resource selection stays here. |
| `provider` | Focused construction/resource injection and close-once lifecycle. No automatic Role creation or deployed grants. |
| Dart / Flutter | Pure-Dart facade and opaque review/status; app-owned plugins, secure storage, explicit signup UI and personal-prefix use. |

Dependency direction remains `provider -> service/authn/oidc/security`, `service -> authn/security/authz`, `authn -> oidc/security`, `oidc -> security`, and `security -> authz`. Core receives no Identity/UID/Role. An exported pure binding value is not an authorization capability; only installed security state supplies production subject bindings.

The extraction is focused on action/role/policy/access/scope/edge_scope/scope_cache/limits and pure matching. Transitional Go aliases may preserve consumer ownership without duplicating models or validation. Do not move state/consensus/session into authz, create another Go module or auth service, or weaken compiled-state corruption tests during type movement.


## UID selector wire format and future grants

The same rule type can later express literal data/read on `public:users:` plus UID-bound data/write on `{literal_before:"public:users:", literal_after:":"}` without per-user Role copies. Such grants must be explicitly configured and assigned; they are not provisioned here. Legacy UID-less users can receive a separate static read Role, not a UID-bound write assignment. Query, CDC, export and receipt disclosure use the same data/read scope; anonymous access remains a separate contract. Edge endpoint checks use the cluster's explicit endpoint policy, independently of Role rules.

The wire rule carries explicit typed resource, read/write permission and one data selector where required. Retired effect/action/global/pair variants are rejected by active decoders, and unknown selector variants cannot become an empty prefix or management resource. Admin and generated Go/Node/Dart/Rust consumers must round-trip supported fields or reject unknown editing. The image-v3/admission contract requires a homogeneous supported cohort. Backward compatibility and migration from earlier models are outside this implementation; incompatible retained material fails closed and is never reset or deleted automatically.

## Registration policy and restricted authority

The typed sys `RegistrationPolicy` is version 2, default absent/disabled, with fixed `personal_user` and at most 32 exact `(issuer, kind, client_profile_id)` profiles sorted by those fields. Kind `native` selects an existing native client profile. Kind `browser` uses the fixed profile ID `browser` and the issuer's exact enabled browser ClientID and configuration revision. Missing and unknown kinds are invalid. Opening/expanding registration requires existing operation-bound trust-expansion reauthentication; closing/reducing is restrictive. Policy changes do not mutate issuer revisions or revoke existing sessions.

Before initial assignment, current policy must permit the profile and the reviewed shared Role must contain only the allowed personal selectors/actions. The operation is bound to that exact policy cut. A disabled policy blocks new UID/grant creation, while existing registered-user idempotent confirmation and properly authorized original-status recovery remain distinct operations.

Unknown users cannot gain ordinary Admission through provisional grants or relaxed lineage checks. The existing current owner issues a closed registration-specific sealed proof/use, bound to verified native evidence or consumed browser Code evidence, exact identity/client/issuer revisions and original times, attempt purpose, policy/cut, and reviewed absent-or-existing state. It authorizes only self registration, never data/admin/another user's operation. Historical verification and current consume accept only the matching closed command/authorization variant. Browser Code evidence cannot enter the native registered-session branch.

## Wire, commit and response-loss contract

| RPC | Contract |
| --- | --- |
| `BeginNativeRegistration` | Bounded native attempt with registration or registration-recovery purpose; recovery binds the original review/intent. Reuse existing affinity/nonce/state/profile machinery. |
| `PrepareNativeRegistration` | Verify provider completion once (including Apple's single code exchange), create bounded opaque proof and exact Server-minted review. No durable registration/session mutation. Recovery returns only proof for the original review, not a replacement review. |
| `ApplyNativeRegistration` | Dispatch exactly the prepared `S1RegisterPrincipal` through existing current/sys machinery; after original Applied and current revalidation, issue the existing native session separately and fence output. |
| `GetNativeRegistrationStatus` | Inspect original ID/intent/actor only; never re-register, issue or recover a raw session credential. |

`S1RegisterPrincipal` commits UID, Principal, initial assignment and verified human enrollment together in one sys operation. Session issuance is a separate existing `S1IssueSession`; profile data is a third independent operation. Do not describe them as one atomic transaction. A trusted registration-to-session transition must verify the original Applied and installed identity/UID/lineage, preserving original evidence and current validity; never fabricate native-login-v2 evidence or repeat Apple exchange.

The native registration-to-session implementation confines the separate session transition to the registration original's exact resulting cut. Any intervening semantic policy or lineage change requires ordinary login; control NOOPs do not change the cut. The final native session response checks both the original registration verification and the installed session at one output-time sample.

On browser first login, `/auth/callback` consumes the ordinary Code/PKCE transaction once, uses its closed evidence to commit an absent Principal's UID/initial assignment, refreshes current authority, then follows the existing browser session issuance and output fence. A known active Principal follows existing login without UID allocation or assignment repair, even when registration is closed. OFF, an unlisted profile, a missing/unsafe initial Role, a disabled issuer or a suspended/deleted identity refuses new enrollment. A possible Apply dispatch with an uncertain result fails the callback without issuing cookies or creating a replacement operation. A fresh login can observe a committed Principal and obtain a separate session. Replaying the consumed callback never recovers session credentials.

The authn flow owner retains only bounded volatile proof material: random opaque 256-bit proof (digest comparison), process/identity/review/purpose binding, expiry at the earlier of the original absolute 10-minute attempt or evidence expiry, maximum 128 verified pending entries and 8 MiB, also subject to current-owner pending bounds. Use existing lifecycle/request cleanup; no new worker or durable credential store. Expiry/cancel/Close discards token/code/producer references without promising physical string zeroization. Atomic Apply ownership starts session issuance at most once; duplicate Apply converges to original status. Preserve no-store, bounded decoding, redaction and output fencing.

- Save the original review before Apply dispatch. An uncertain dispatch remains status-first; not-found is unresolved, never rollback or permission to restamp/replay.
- After process/app restart or proof expiry, freshly authenticate the same Identity under recovery purpose and inspect the retained original review. Another account cannot recover it. A fresh proof cannot rewrite the old ID/intent/time.
- If registration Applied but session issuance/output is lost, preserve registration, report original status subject to current disclosure, then use ordinary login for a new session. Never recover a lost raw credential from status.
- Prepare response loss has no durable registration dispatch. Do not reuse/exchange a consumed provider code. Data profile Put loss uses its own original receipt and `mayHaveDispatched` state.
- Extend original-disclosure checks narrowly for the same registration actor/command/intent, including restricted unknown-user recovery and registered user's native-session self status. IDs alone disclose nothing.

## Lifecycle and integration

Revalidate current cut/time/issuer/client/state at Prepare, consume, Apply, post-registration, session issuance and final output. Preserve signed `auth_time`; UID creation is not fresh authentication. Sessions retain their absolute eight-hour lifetime with no refresh. Policy closure stops new signup; Principal suspension/deletion, assignment withdrawal, session revocation and issuer/client disable govern existing access. Deleted tombstones retain UID, remove assignments and revoke sessions; data removal remains a separate explicit operation/TTL lifecycle, and UIDs are never reused.

Keep existing endpoint/verified identity/client-profile offline partitions when adding UID, so old uncertain outboxes do not become orphaned. Account changes cancel/join/close old work; pending registration reviews are identity-partitioned and cannot resume under another account. Flutter owns plugins/secure storage/signup UI and uses the returned personal prefix; the SDK stays pure Dart with private generated types and existing TokenProvider.

The active versioned registration proof/selector contract is bound by admission/provisioning fingerprints. Update image/capsule bounds, replay/checkpoint, projection/fence digests and capacity charges together. Unsupported old models, configurations and journals fail closed. Startup and registration never rewrite incompatible retained material, reset journals or delete data.

Existing UID-less Principals remain UID-less until explicit native signup; browser login leaves known users unchanged. Fresh OIDC provisioning includes the safe `personal_user` template and disabled version-2 policy. Ordinary startup never creates missing Roles, assigns users or opens registration. Current 4 MiB sys image and 10,000 Principal/session caps remain; signup uses expanding capacity, preserves restrictive reserve and fails atomically at limits. Before actual enablement inspect collisions and explicit broad grants under `users:`. Admin must round-trip PrincipalUIDPrefix and RegistrationPolicy or reject unsupported editing, never convert them to empty selectors or silently remove policy.

The remaining product/activation decision is **which exact provider profiles, if any, this deployment will open for self-registration**; ask before actual activation. Revision r2 is settled and default-disabled implementation proceeds without another design approval. Invitations, email linking, generic ABAC, quotas/sharding, another database/auth server, provider creation, deployment and publication are outside this Issue.

## Delivery and finite validation

1. Extract pure authz with unchanged existing semantics/canonical bytes and paired tests.
2. Implement typed selectors/bindings/caches, UID uniqueness and RegistrationPolicy/sys transitions.
3. Implement restricted purpose-bound registration, original-status recovery and existing session workflow transition.
4. Add pure-Dart/app signup, durable review-first recovery, returned-prefix use, Admin/generated consumers and complete design/operator docs.
5. Freeze the cohesive candidate; run required repository gates/functional-validation/configured-source-integration, targeted safety-boundary review when warranted, PR CI, normal merge and exact-main checks. Resume selected provider/device checks only afterward against actual personal prefixes.

Finite cases cover: sequential/concurrent signup and UID collision; active/suspended/deleted users; no assignment resurrection; closed policy/capacity/restart; A/B Role/cache/Snapshot isolation; delimiter/missing UID/unknown selector/Explain/non-expansion; personal-root CRUD/TTL/query/refused mixed batches/Edges/CDC/cursors/receipts/export; login/register/recovery mix-up and invalid provider/proof/current evidence; Apple exchange once; response-loss boundaries and output fence; real browser and native signup/session/self data/other-user refusal; retained uncertain IDs; Admin round trips; a focused binding/auth benchmark. Include static public read/self write/other-user refusal, suffix and byte boundaries, UID-less users, cache/CDC separation, configured Edge direction and unknown wire/Admin variants. Extend existing harnesses with bounded positive/negative cases, then run the coordinated gate once on the cohesive candidate.

## Client use and installation boundary

All clients can read the authenticated Principal's installed ID through the
existing `GetCurrentPrincipal` self-inspection RPC, without a data or management
Role. The wire field is optional `user_id`; it is the same immutable ID after
registration, ordinary login and restoration. It is not a provider subject,
email, client-generated key, access grant or a new authentication factor.

| Consumer | Current-source API | ID absent |
|---|---|---|
| Go | `GetCurrentUserID(ctx)` on `Lantern` or `Failover` | `nil` |
| Node / browser | `SecurityClient.getCurrentUserId()`; `currentUserId(principal)` for a browser session | `undefined` |
| Dart | `LanternClient.getCurrentUserId()`; `NativeSession.userId` | `null` |
| Rust | `LanternClient::current_user_id(CallOptions)` | `None` |
| Admin | Ready auth state's `principal.userId` | `undefined` |
| CLI | `lantern-cli whoami` | JSON `{"user_id":null}` |
| MCP | `current_user` | Structured `{"user_id":null}` |

Authenticated UID-less legacy and machine Principals have no ID. Missing or
expired authentication returns the existing authentication error; clients do not
return a prior account's cached ID. Malformed present IDs and machine IDs are
refused. Self-inspection makes one attempt and does not register, change Roles,
refresh credentials or resurrect assignments. Session metadata belongs to that
session; account changes clear the app's active session before new work starts.
MCP reports only its configured Lantern backend credential's Principal, never
the conversation user or announced agent. These APIs are current source and
require compatible Server/SDK builds; package publication remains separate.


The Dart public `NativeSession` exposes nullable `userId` and `personalPrefix`.
A successful registration with a disclosed session returns them through
`NativeRegistrationResult.sessionCredential.session`. Ordinary
`completeNativeLogin().session` and `getNativeSession().session` return the same
installed values after re-login or app restoration. Legacy Principals without a
UID return null for both until explicit registration. `NativePrincipal.subject`
remains the exact OIDC subject and is not the Lantern UID. A prepared review's UID
is only a proposal; consumers use session metadata after installation, and every
operation is authorized against the Server's current Principal binding.

The app calls Begin → provider plugin → Prepare, saves `prepared.review.toBytes()`
before Apply, and stores a disclosed session credential through its own secure
store. After uncertain Apply, restore `NativeRegistrationReview.fromBytes`, begin
registration with `recoveryReview`, authenticate the same provider identity and
Prepare a recovery proof, then call `getNativeRegistrationStatus`. Unknown stays
unresolved. Applied without a session requires ordinary sign-in. Recovery proofs
cannot Apply; neither session credentials nor provider tokens appear in review
bytes. The example uses the returned personal prefix for data keys while retaining
its existing endpoint/identity/profile SQLite partition.

The current-v2 transport version remains 2, but its credential and purpose
contracts now include registration evidence v3 and the trusted UID selector/bound
scope semantics. Their admission profile digest feeds the origin descriptor,
protocol scope, signed membership/provisioning and existing custody checks. Old
provisioning profiles are rejected; this change does not claim mixed-version
rollout or rewrite old journals. Operator rollout requires the existing coordinated
provisioning/custody procedure for the new exact contract. Retained development
journals and OAuth clients are not reset by implementation or tests.
