# Authorization model 3

This is the active RPC/resource contract for OIDC mode. A Principal receives
permissions only through assigned Roles. A rule grants exactly `read` or `write`
on `data`, `security`, `settings`, `operations` or `schema`. Matching rules from
all assigned Roles form a union; no matching rule means refusal. `write` does
not imply `read`. Operations and schema expose only `read`, so their `write`
rules are invalid.

A Data rule has exactly one literal logical prefix or trusted immutable
user-UID prefix selector. The empty literal prefix explicitly means all Data;
`*` is literal. Other resources have no prefix or personal selector. There is
no Effect/Deny field, fine-action grant, wildcard action, direct-user permission
or Role-specific Edge pair. Internal operation names remain exact admission
and original receipt identities; they map centrally to this grant vocabulary.

## Data RPCs

Every exact batch is authorized completely before resource lookup or mutation.
Collections select the authorized positive subset before limits, counts, paths,
ranking and cursor generation. Data keys remain logical public identities;
physical `sys:` storage and peer replication are outside user Data grants.

| RPC | Resource / permission | Scope and disclosure |
| --- | --- | --- |
| `GetVertex`, `GetVertices` | Data / read | Every exact key, including missing results |
| `PutVertex`, `PutVertices` | Data / write | Every exact key; includes replacement, conditional Put, TTL shortening and born-expired effects; detail needs read on every key |
| `DeleteVertex`, `DeleteVertices` | Data / write | Every exact key; detail needs read on every key; incident-edge cleanup is lifecycle maintenance |
| `ScanVertices`, `ScanVertexKeys`, `CountVerticesByPrefix` | Data / read | Filter before pagination, missing identities and counts |
| `SearchVertices` | Data / read | Filter candidates before top-k; ranking retains the existing shared application corpus |
| `Illuminate`, `TopVerticesByDegree` | Data / read | Readable induced graph; hidden endpoints contribute no path or actual degree |
| `DeleteVerticesByPrefix` | Data / write | Bounded authorized victims; dry-run and actual counts need the whole selected policy range readable |
| `GetEdge`, `GetEdges`, `ScanEdges` | Data / read | Both endpoints, including missing results |
| `CreateEdge`, `CreateEdges` | Data / global endpoint mode | Existing live endpoints only; HA Create remains unavailable pending its independent arbitration qualification |
| `AddEdge`, `AddEdges`, `PutEdge`, `PutEdges` | Data / global endpoint mode | Existing live endpoints in protected mode; no endpoint creation or Vertex value/TTL change |
| `DeleteEdge`, `DeleteEdges`, `DeleteEdgeContribution`, `DeleteEdgeContributions` | Data / global endpoint mode | Exact requested pair/contribution; detail additionally needs both endpoint reads |
| `DeleteEdgesByPrefix` | Data / global endpoint mode | Selected endpoint filters before lookup and in the final Core view; dry-run/counts require both complete endpoint policy ranges readable |
| `MutateGraph` | Data / write for Vertex items; global endpoint mode for Edge items | One atomic mixed publication; one unreadable item redacts the complete call |
| `BackupSnapshot` | Data / read | Logical data only; referentially closed readable vertices and edges; no private security or raw peer Snapshot |
| `WatchChanges` | Data / read | Explicit identity/value projection; both endpoints for Edge invalidations; no hidden raw log progress or receipt contents |
| `GetReceiptCapability` | Data / read **or** write in any nonempty scope | Static supported shapes and durability capability; no permission to mutate a particular key or inspect a receipt |
| `GetReceiptStatus`, `GetReceiptStatuses` | Data / read ingress **and current original mutation authority** | Exact retained original resource/ID safeguards remain; an ID or caller-supplied resource is not authority; unknown IDs require whole-domain read or fail closed |
| Every receipt-bearing mutation/replay | The original mutation's current resource permission | Receipt context adds no read prerequisite to handling; original detailed effects additionally require every original resource readable |
| `GetServerStatus`, `GetReplicationStatus` | Operations / read | No implicit Data, Security or Schema grant |

The Edge policy is one native, immutable-per-request cluster setting:

| Mode | Tail requirement | Head requirement |
| --- | --- | --- |
| `rr` (fresh default) | Data read | Data read |
| `ww` | Data write | Data write |
| `rw` | Data read | Data write |
| `wr` | Data write | Data read |

Every Edge modification and TTL change uses the same selected mode. Endpoint
grants may come from different Roles. A reader can therefore edit connections
under `rr`, while remaining unable to write either Vertex. Edge result disclosure
always requires read on both endpoints, independently of the mutation mode.
A mode change uses Security write and the ordinary native CAS/original-operation
path; admitted output and cached scopes bind the captured mode.

When any selected mutation resource is unreadable, the complete reply contains
only `MutationAcceptance{HANDLED_EFFECT_UNDISCLOSED}`. All outcome, count,
existence, weight and unknown fields are cleared. This choice depends on policy
and public input, not private stored state. The signal proves handling without
asserting application, creation, deletion, liveness or condition success.
Private conditional/receipt conflicts use that same projection. Invalid input,
authority, transport and durability errors keep their normal failure contract.
Stored authoritative receipt results and original operation IDs never change.
Clients must recognize acceptance explicitly and must not infer an effect from
empty fields or automatically retry an acknowledged operation.

## Security and Settings RPCs

Selector-free management resources stay separate from Data and from each other.
Qualified machine credentials may perform granted reference/status reads;
management mutations require a qualified end-user. Valid credentials, session
expiry, current admission, exact origin/CSRF, expected full cut, native CAS,
environment-owned locks and last-usable-administrator invariants still apply.

| RPC / operation | Resource / permission | Additional boundary |
| --- | --- | --- |
| `GetAuthCapabilities` | Public sanitized capability | Mode/readiness/features only; errors never imply OFF |
| `GetCurrentPrincipal` | Current authenticated self | Exact identity, own effective grant view and optional verified display email; no permission to inspect another Principal |
| `ListIssuers`, `GetIssuer`, `ListRoles`, `GetRole`, `ListUsers`, `GetUser`, `ListRoleAssignments`, `ListSecurityAudit`, `GetRoleTemplates` | Security / read | Bounded references and projections; no secret disclosure |
| `GetRegistrationPolicy`, `GetEdgePolicy` | Security / read | Registration and Edge policy are Security resources |
| `ExplainAccess` | Security / read | Server evaluates typed resource/permission or an explicit derived Edge operation; diagnostic operation names are not grants |
| `ValidateIssuer` | Security / read | Qualified end-user plus bounded conservative network probe; machine probe forbidden |
| `GetLogSettings`, `GetOperationalSettings`, `PreviewOperationalPolicy` | Settings / read | Desired/applied state or non-mutating bounded preview |
| `PrepareSecurityChanges`, `ApplySecurityChanges`, `ApplySecurityChange`: issuer, Role, Principal, assignment, session revocation, registration policy or Edge policy changes | Security / write | Exact validated native command; singular Apply uses the plural canonical operation |
| The same Prepare/Apply RPCs: log or operational policy changes | Settings / write | Works independently of Security write; mixed-resource batches require both writes |
| `BeginSecurityChangeAuthorization`, `GetSecurityChangeAuthorization` | Required write resource(s) of the exact reviewed command | Qualified end-user, exact actor/original ID/intent/full cut and operation-bound signed post-review event |
| `GetSecurityChangeStatus` and original result reconciliation | Relevant Security/Settings read plus exact original-operation guards | Original actor, ID, intent, state and current-resource checks; no cross-resource shortcut |
| Trusted issuer change or effective Security/Settings write expansion | Required command write resource(s) plus purpose proof | Ordinary Data expansion has no extra reauthentication gate |

Browser login/logout and native session/registration endpoints authenticate or
operate on the exact self/original operation. They do not infer Role grants from
email, groups, subject spelling or client input. `BeginNativeLogin`,
`CompleteNativeLogin`, `GetNativeSession`, `PrepareNativeLogout`,
`ApplyNativeLogout`, `BeginNativeRegistration`, `PrepareNativeRegistration`,
`ApplyNativeRegistration` and `GetNativeRegistrationStatus` retain their typed
credential, nonce, issuer/profile, self and original-operation checks. First
registration additionally needs the explicit enabled registration policy and
matching native/browser profile. Browser enrollment never follows from a native
profile. A known active user login does not reassign Roles.

HTTP metrics and operational diagnostics require Operations read. Public
reflection requires Schema read in OIDC. Health remains content-free and
structurally authentication-exempt while readiness checks its serving owner.
Private `Subscribe`, `Snapshot`, `PeerStatus` and control quorum/origin routes
use separate workload trust and custody; no user Role grants peer membership.
An unclassified new RPC fails closed until this central contract is extended.

## Version boundary and verification

Model 3 uses the new rule schema, explicit execution/model fingerprint and fresh
native image. Previous APIs, generated SDK wire rules, Effect/action/Deny data,
configuration and signed native histories are unsupported. They fail closed;
backward compatibility and migration are not required for this change. No
automatic conversion, permission expansion, deletion or reset of existing state
is authorized or performed. Operators must not treat rejection as a reset or
reuse old files as a fresh environment.

Focused source tests cover typed union/default refusal, management separation,
UID scopes, global mode truth tables, Core prefix filters and complete-call
response redaction. Actual OIDC Connect tests cover read-only/write-only/union
Roles, TTL shortening, conditional/born-expired Put, blind mutation/replay and
later detailed disclosure. Broad integrated gates run once on a coherent source
candidate; provider/device/Kubernetes qualifications retain their independent
status.
