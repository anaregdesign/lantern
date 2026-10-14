# Native user registration and personal authorization

Accepted design, revision r2 with selector supplement r2.1 (2026-10-14), for [#1758](https://github.com/anaregdesign/lantern/issues/1758). This document defines the target contract; deployment registration remains disabled until explicitly configured.

## Outcome

Register users through an explicit native Google/Apple signup operation and provide one personal area for editable metadata and general-purpose application data. Preserve module responsibilities and maintainability. Avoid static Role proliferation per user; choose the justified architecture rather than minimizing the diff.

The design was reviewed against source `fd531f9efe980eaceea3b3bce0d70560d30256e3` on 2026-10-14 and uses **one shared `personal_user` Role whose typed personal-area selectors bind to a trusted immutable Principal UID**. It participates in the existing PLS semantics; there is no independent ownership bypass or mutable-record ACL. Pure Role/Access/Scope evaluation moves to `internal/authz`; durable state, consensus, credential evidence, current admission, revocation and output fencing remain with the existing single `security` owner.

**Current prefix decision: revision r2 (2026-10-14), accepted after the owner's requested five-use-case comparison. Use one personal root `users:<uid>:`. Metadata and general application data may be organized semantically beneath it; they have the same personal permissions and do not need separate Server roots. The original two-root layout is retained only in the history below.**


**Selector revision r2.1 is accepted:** use one typed `PrincipalUIDPrefix` with fixed `literal_before` and `literal_after`, inserting only the installed Principal's trusted UID. This replaces the proposed `PersonalArea.DATA` enum as the rule representation. It is neither a users-specific evaluator shortcut nor an arbitrary template language. Initial registration grants and the returned personal prefix remain only `users:<uid>:`; public grants and anonymous access are not added.

## Identity, registration and namespaces

- Add optional trusted `Principal.UserUID`: a Server-generated nonzero random 128-bit value, rendered as exactly 32 lowercase hex digits. Callers and ordinary `PutPrincipal` cannot choose or change it. Validate both exact Identity-to-Principal and UID-to-Identity uniqueness, including deleted tombstones. Machine Principals cannot carry a UID.
- Bind identity to the existing verified exact OIDC `(issuer, subject)`. No email linking, plugin-supplied identity, arbitrary claims, API token or machine credential as signup evidence. Preserve the actual Google nonce/azp profile and original signed authentication-time semantics.
- Unknown Identity: atomically allocate UID, Active Principal, first shared Role assignment and verified human-enrollment fact. Active legacy Principal without UID: explicit signup adds UID/assignment without overwriting existing grants or Denies. Active Principal with UID: return the same UID and **never restore a withdrawn assignment**. Suspended/deleted: refuse signup and session issuance; do not resurrect or allocate a replacement UID.
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

There is no atomic rename/move RPC; cross-prefix copy/delete is a new key/intent and does not automatically migrate Edges, receipts, CDC history or offline outboxes. Suspension and physical data deletion remain separate, and one-root deletion is not complete account erasure. Export remains ungranted; any future backup needs its existing Read/Export and Edge endpoint checks. Public/shared functionality is illustrative future scope, not an added completion requirement.

## Shared policy and bound authorization

Add a typed `PrincipalUIDPrefix { literal_before, literal_after }` data selector, mutually exclusive with existing LiteralPrefix and Global. Bind `literal_before + installedPrincipal.UserUID + literal_after`. Require a nonempty UTF-8 `literal_before` ending in `:` and a nonempty UTF-8 `literal_after` beginning with `:`. The UID is canonical 32-digit lowercase hex from trusted installed state, never request/plugin/metadata input. No UID means binding rejection; machine assignment is invalid. Literal text stays byte-literal, with no normalization, recursive expansion, glob, expressions, arbitrary variable/claim paths or multiple slots. Existing literal `users:<uid>:` remains a literal string, never retroactively expanded.

Enforce the configured prefix limit (hard maximum 1,024 bytes) on `before + 32 UID bytes + after`, and the 1 MiB whole-policy bound on equivalent expanded bytes. Include both literals and UID width before marshaling/cache-key allocation; retain rule/assignment/image/capsule/cache bounds. Unknown/competing variants and global-action use fail closed. Role administrators choose the fixed literals; ordinary data callers choose neither template nor evaluated UID.

The initial shared Role allows `vertex.read`, `vertex.write`, `vertex.delete`, `query`, `cdc.identity`, `cdc.value`, and `receipt.read` only with `{literal_before:"users:", literal_after:":"}` (seven rules total). Its initial-assignment validator must reject arbitrary UID-bound roots/suffixes or extra Allow actions, not assume any UID selector is safe. It grants no export, operations, schema, security or replication capability.

Preserve the union of assigned Allows minus all Denies. Literal and personal Denies, `Allows`, `Scope`, `AllowsAll`, Explain, non-expansion checks and blind-response decisions use the same bound selector semantics. Existing explicit operator data grants still compose normally; signup does not silently revoke them.

Keep current TTL/Delete checks, denied-batch atomicity, pre-top-k/query filtering, Edge endpoint rules (both reads for read; tail read plus head write for modification), HA Create fail-closed behavior, CDC action intersections and receipt original-resource/action/disclosure checks. Do not create UID-specific ranking statistics or weaken unknown-receipt global-proof requirements.

Compile Role structure once and bind immutable lightweight Access views using trusted `SubjectBindings{PersonalUID}`. Include the canonical binding digest in Access reuse, Scope and Edge candidate cache keys, admission scope binding and cursor/CDC authority. Prevent A/B leakage through the current Role-set-only cache and previous Snapshot reuse. Retain bounded derived caches (256 entries / 8 MiB); do not create unbounded UID maps or per-user copies of persisted Role rules. Only installed security state constructs production bindings; service request UIDs never do so. Keep `authz` standard-library-only and free of I/O, clock and Principal/session state.

## Package ownership and dependency direction

| Package | Responsibility |
| --- | --- |
| `internal/authz` | Pure Role/Action/selectors, compile/bind, Allow/Deny, Access/Scope/Edges, bounded derived caches and Explain; pure UID syntax/prefix derivation. Standard library only; no state, I/O or clocks. |
| `internal/security` | Principal/UID uniqueness, stored Role/assignments and RegistrationPolicy, durable sys ledger/consensus, restricted registration authority, current admission/revocation/output/custody. One durable owner. |
| `internal/oidc` | Provider verification, purpose-bound transactions and opaque completions, Apple's one-time exchange. No UID allocation or grants. |
| `internal/authn` | Registration workflow and bounded volatile proofs, Prepare/Apply/status, trusted producer composition and transition into existing session issuance. |
| `service` | Strict Connect DTO/oneof/headers, affinity, limits/redaction and current output adapter. Existing data action/resource selection stays here. |
| `provider` | Focused construction/resource injection and close-once lifecycle. No automatic Role creation or deployed grants. |
| Dart / Flutter | Pure-Dart facade and opaque review/status; app-owned plugins, secure storage, explicit signup UI and personal-prefix use. |

Dependency direction remains `provider -> service/authn/oidc/security`, `service -> authn/security/authz`, `authn -> oidc/security`, `oidc -> security`, and `security -> authz`. Core receives no Identity/UID/Role. An exported pure binding value is not an authorization capability; only installed security state supplies production subject bindings.

The extraction is focused on action/role/policy/access/scope/edge_scope/scope_cache/limits and pure matching. Transitional Go aliases may preserve consumer ownership without duplicating models or validation. Do not move state/consensus/session into authz, create another Go module or auth service, or weaken compiled-state corruption tests during type movement.


## UID selector wire format and future grants

The same rule type can later express literal read on `public:users:` plus UID-bound write/delete on `{literal_before:"public:users:", literal_after:":"}` without per-user Role copies. Such grants must be explicitly configured and assigned; they are not provisioned here. Legacy UID-less users can receive a separate static read Role, not a UID-bound write assignment. Query/CDC/receipt permissions remain separately required, and anonymous access remains a separate contract. With shared public reads, existing Edge rules may allow another user's tail to one's own head, while another user's head remains denied by self-only write; do not claim both endpoints must always be self-owned.

Add a new message variant at an unused `SecurityRule.resource` oneof tag, preserving existing prefix/global/pair tag meanings and rejection of retired pair selectors. Encode/decode the new variant explicitly: absence of old Prefix/Pair must not fall back to Global. Internal new fields have presence and omit absent values, preserving old Role/Image/S1 canonical bytes. Admin and generated Go/Node/Dart/Rust consumers must round-trip or reject unknown editing; no unknown-to-global/empty-prefix downgrade. Version the selector contract and existing admission/provisioning/custody boundaries without claiming transparent old-peer compatibility. No interim selector was persisted or published, so implement the final type directly and do not move existing data keys.

## Registration policy and restricted authority

Add a separate typed sys `RegistrationPolicy`, default absent/disabled, with fixed `personal_user` and a bounded sorted allowlist of exact `(issuer, native_client_profile_id)` pairs (maximum 32). Opening/expanding registration requires existing operation-bound trust-expansion reauthentication; closing/reducing is restrictive. Policy changes do not unnecessarily mutate issuer revisions or revoke existing sessions.

Before initial assignment, current policy must permit the profile and the reviewed shared Role must contain only the allowed personal selectors/actions. The operation is bound to that exact policy cut. A disabled policy blocks new UID/grant creation, while existing registered-user idempotent confirmation and properly authorized original-status recovery remain distinct operations.

Unknown users must not gain ordinary Admission through `skipActive`, provisional administrator grants, or relaxed lineage checks. The existing current owner issues a closed registration-specific sealed proof/use, bound to verified native evidence, exact identity/client/issuer revisions and original times, attempt purpose, policy/cut, and the reviewed absent-or-existing state. It authorizes only self registration Prepare/Apply/original status and restricted output, never data/admin/another user's operation. Historical verification and current consume must accept only the matching closed command/authorization variant.

## Wire, commit and response-loss contract

| RPC | Contract |
| --- | --- |
| `BeginNativeRegistration` | Bounded native attempt with registration or registration-recovery purpose; recovery binds the original review/intent. Reuse existing affinity/nonce/state/profile machinery. |
| `PrepareNativeRegistration` | Verify provider completion once (including Apple's single code exchange), create bounded opaque proof and exact Server-minted review. No durable registration/session mutation. Recovery returns only proof for the original review, not a replacement review. |
| `ApplyNativeRegistration` | Dispatch exactly the prepared `S1RegisterPrincipal` through existing current/sys machinery; after original Applied and current revalidation, issue the existing native session separately and fence output. |
| `GetNativeRegistrationStatus` | Inspect original ID/intent/actor only; never re-register, issue or recover a raw session credential. |

`S1RegisterPrincipal` commits UID, Principal, initial assignment and verified human enrollment together in one sys operation. Session issuance is a separate existing `S1IssueSession`; profile data is a third independent operation. Do not describe them as one atomic transaction. A trusted registration-to-session transition must verify the original Applied and installed identity/UID/lineage, preserving original evidence and current validity; never fabricate native-login-v2 evidence or repeat Apple exchange.

The authn flow owner retains only bounded volatile proof material: random opaque 256-bit proof (digest comparison), process/identity/review/purpose binding, expiry at the earlier of the original absolute 10-minute attempt or evidence expiry, maximum 128 verified pending entries and 8 MiB, also subject to current-owner pending bounds. Use existing lifecycle/request cleanup; no new worker or durable credential store. Expiry/cancel/Close discards token/code/producer references without promising physical string zeroization. Atomic Apply ownership starts session issuance at most once; duplicate Apply converges to original status. Preserve no-store, bounded decoding, redaction and output fencing.

- Save the original review before Apply dispatch. An uncertain dispatch remains status-first; not-found is unresolved, never rollback or permission to restamp/replay.
- After process/app restart or proof expiry, freshly authenticate the same Identity under recovery purpose and inspect the retained original review. Another account cannot recover it. A fresh proof cannot rewrite the old ID/intent/time.
- If registration Applied but session issuance/output is lost, preserve registration, report original status subject to current disclosure, then use ordinary login for a new session. Never recover a lost raw credential from status.
- Prepare response loss has no durable registration dispatch. Do not reuse/exchange a consumed provider code. Data profile Put loss uses its own original receipt and `mayHaveDispatched` state.
- Extend original-disclosure checks narrowly for the same registration actor/command/intent, including restricted unknown-user recovery and registered user's native-session self status. IDs alone disclose nothing.

## Lifecycle, migration and integration

Revalidate current cut/time/issuer/client/state at Prepare, consume, Apply, post-registration, session issuance and final output. Preserve signed `auth_time`; UID creation is not fresh authentication. Native sessions remain absolute eight hours with no refresh. Policy closure stops new signup; Principal suspension/deletion, assignment withdrawal/Deny, session revocation and issuer/client disable govern existing access. Deleted tombstones retain UID, remove assignments and revoke sessions; data removal remains a separate explicit operation/TTL lifecycle, and UIDs are never reused.

Keep existing endpoint/verified identity/client-profile offline partitions when adding UID, so old uncertain outboxes do not become orphaned. Account changes cancel/join/close old work; pending registration reviews are identity-partitioned and cannot resume under another account. Flutter owns plugins/secure storage/signup UI and uses the returned personal prefix; the SDK stays pure Dart with private generated types and existing TokenProvider.

Preserve old serialized Role/Image/operation bytes during pure extraction; omit absent new fields, especially in canonical `S1Command` transcripts. Add a versioned registration proof/selector contract to admission/provisioning fingerprints; never reinterpret old history or silently admit unknown peers. Update image/capsule bounds, replay/checkpoint, projection/fence digests and capacity charges together using existing coordinated provisioning/custody. No journal resets or claims of safe mixed-version rollout by field addition alone.

Old Principals remain UID-less until explicit signup. Do not auto-create assignments or enable registration at startup. Current 4 MiB sys image and 10,000 Principal/session caps remain; signup uses expanding capacity, preserves restrictive reserve and fails atomically at limits. Before actual enablement inspect collisions/explicit broad grants under `users:` and choose controlled migration if needed. Regenerate Go/Node/Dart/Rust from proto. Admin must round-trip PrincipalUIDPrefix and RegistrationPolicy or reject unsupported editing, never convert them to empty literal/global selectors or silently remove policy.

The remaining product/activation decision is **which exact provider profiles, if any, this deployment will open for self-registration**; ask before actual activation. Revision r2 is settled and default-disabled implementation proceeds without another design approval. Invitations, email linking, generic ABAC, quotas/sharding, another database/auth server, provider creation, deployment and publication are outside this Issue.

## Delivery and finite validation

1. Extract pure authz with unchanged existing semantics/canonical bytes and paired tests.
2. Implement typed selectors/bindings/caches, UID uniqueness and RegistrationPolicy/sys transitions.
3. Implement restricted purpose-bound registration, original-status recovery and existing session workflow transition.
4. Add pure-Dart/app signup, durable review-first recovery, returned-prefix use, Admin/generated consumers and complete design/operator docs.
5. Freeze the cohesive candidate; run required repository gates/F/Q, targeted safety-boundary review when warranted, PR CI, normal merge and exact-main checks. Resume selected provider/device checks only afterward against actual personal prefixes.

Finite cases cover: sequential/concurrent signup and UID collision; legacy active/suspended/deleted users; no assignment resurrection; closed policy/capacity/restart; A/B Role/cache/Snapshot isolation and churn; delimiter/missing UID/unknown selector/Deny/Explain/non-expansion; personal-root arbitrary-subkey CRUD/TTL/query/mixed denied batches/Edges/CDC/cursors/receipts; login/register/recovery mix-up and invalid provider/proof/current evidence; Apple exchange once; every described response-loss boundary and output fence; real-wire signup/session/self data/other-user denial; SQLite A→B→A retaining uncertain IDs; Admin round trips; a focused hot-path binding/auth benchmark. Integrate finite r2.1 controls for static public read/self write/other-user refusal, UID/literal Denies, suffix and byte boundaries, UID-less users, cache/CDC separation, Edge direction, unknown wire/Admin variants and unchanged old canonical bytes. These are rule-evaluator fixtures, not a public-feature or provider/device campaign. Extend existing harnesses with bounded positive/negative cases, without an unbounded load/soak or duplicate reviewer campaign.
