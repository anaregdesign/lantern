import {
  copySecurityContract,
  validateCurrentReview,
  type EdgeEndpointMode,
  type SecurityIdentity,
  type SecurityRegistrationPolicy,
} from "lantern-sdk/web";
import type { CurrentChangeState } from "../current-change/current-change";
import type { IssuerDraft, RoleDraft, UserDraft } from "./security-drafts";
import type { SecuritySection } from "./security-management";

export interface SecurityEditorIntent {
  mode: "create" | "edit";
  selectedKind: SecuritySection;
  selectedVersion: string;
  issuerKey?: string;
  roleKey?: string;
  userKey?: SecurityIdentity;
  issuer: Omit<
    IssuerDraft,
    "locked" | "existing" | "hasSecretBinding" | "nativeClients"
  >;
  role: Omit<RoleDraft, "locked" | "existing">;
  user: Omit<UserDraft, "existing" | "assignments">;
  templateName: string;
  roleSource: "blank" | "template";
}
export interface SecurityPolicyIntent {
  edgeDraft?: EdgeEndpointMode;
  enrollmentDraft?: SecurityRegistrationPolicy;
  edgeVersion: string;
  enrollmentVersion: string;
}
export interface SecurityDraftIntent {
  binding: string;
  editor?: SecurityEditorIntent;
  policies?: SecurityPolicyIntent;
  policyLoaded: boolean;
  review?: CurrentChangeState["review"];
}

/** Retained intent is not permission, a displayed record, or an Apply queue. */
export function recoverableSecurityReview(
  value: CurrentChangeState["review"],
): CurrentChangeState["review"] {
  if (!value?.contract) return;
  validateCurrentReview(value.contract);
  const review = copySecurityContract(value);
  review.authorizationProof = undefined;
  if (review.approval === "approved")
    review.approval =
      review.authorizationId && review.attemptAffinity
        ? "authenticating"
        : "required";
  if (review.approval === "starting") {
    review.approval = "required";
    review.authorizationId = undefined;
    review.attemptAffinity = undefined;
  }
  return review;
}

/** One app lifetime, three sections, 1 MiB total, and an absolute 30-minute TTL. */
export class SecurityDraftRecovery {
  private owner?: object;
  private readonly entries = new Map<
    SecuritySection,
    { created: number; intent: SecurityDraftIntent }
  >();
  constructor(private readonly now = Date.now) {}
  private select(owner: object) {
    if (this.owner !== owner) {
      this.entries.clear();
      this.owner = owner;
    }
    for (const [section, entry] of this.entries)
      if (
        this.now() - entry.created >= 30 * 60_000 ||
        this.now() < entry.created
      )
        this.entries.delete(section);
  }
  read(
    owner: object,
    section: SecuritySection,
  ): SecurityDraftIntent | undefined {
    this.select(owner);
    const value = this.entries.get(section)?.intent;
    return value && copySecurityContract(value);
  }
  write(owner: object, section: SecuritySection, intent: SecurityDraftIntent) {
    this.select(owner);
    if (!["issuers", "users", "roles"].includes(section)) return;
    const value: SecurityDraftIntent = {
      binding: intent.binding,
      editor: copySecurityContract(intent.editor),
      policies: copySecurityContract(intent.policies),
      policyLoaded: intent.policyLoaded,
      review: recoverableSecurityReview(intent.review),
    };
    const entry = {
      created: this.entries.get(section)?.created ?? this.now(),
      intent: value,
    };
    this.entries.set(section, entry);
    const bytes = new TextEncoder().encode(
      JSON.stringify([...this.entries], (_, item: unknown) =>
        typeof item === "bigint"
          ? item.toString()
          : item instanceof Uint8Array
            ? [...item]
            : item,
      ),
    ).length;
    if (bytes > 1024 * 1024) this.entries.delete(section);
  }
  clear(owner?: object, section?: SecuritySection) {
    if (owner && this.owner !== owner) return;
    if (section) this.entries.delete(section);
    else {
      this.entries.clear();
      this.owner = undefined;
    }
  }
}
