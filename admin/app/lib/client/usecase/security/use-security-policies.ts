import { useEffect, useRef } from "react";
import { useSecurityDraftField } from "./use-security-draft-field";
import type { SecurityPolicyIntent } from "./security-draft-recovery";
import {
  EdgeEndpointMode,
  currentSecurityVersionBinding,
  type SecurityChange,
  type SecurityRegistrationPolicy,
  type SecurityRegistrationProfile,
} from "lantern-sdk/web";
import { change } from "./security-drafts";
import {
  EDGE_MODES,
  enrollmentChoices,
  enrollmentProfileKey,
  registrationDraft,
} from "./security-policy-drafts";
import type {
  SecurityManagementController,
  SecurityManagementState,
} from "./security-management";

export function useSecurityPolicies(
  controller: SecurityManagementController,
  state: SecurityManagementState,
) {
  const fields = useRef<Record<string, unknown>>({});
  const restoring = useRef(false);
  const retain = () => {
    if (restoring.current) return;
    const value = fields.current as unknown as SecurityPolicyIntent;
    controller.retainPolicyIntent({
      edgeDraft: value.edgeDraft,
      enrollmentDraft: value.enrollmentDraft,
      edgeVersion: value.edgeVersion,
      enrollmentVersion: value.enrollmentVersion,
    });
  };
  const [edgeDraft, setEdgeDraft] = useSecurityDraftField<EdgeEndpointMode>(
    "edgeDraft",
    fields,
    retain,
  );
  const [enrollmentDraft, setEnrollmentDraft] =
    useSecurityDraftField<SecurityRegistrationPolicy>(
      "enrollmentDraft",
      fields,
      retain,
    );
  const [edgeVersion, setEdgeVersion] = useSecurityDraftField(
    "edgeVersion",
    fields,
    retain,
    "",
  );
  const [enrollmentVersion, setEnrollmentVersion] = useSecurityDraftField(
    "enrollmentVersion",
    fields,
    retain,
    "",
  );
  const [error, setError] = useSecurityDraftField("error", fields, retain, "");
  const restored = state.restoredIntent?.policies;
  useEffect(() => {
    if (!restored) return;
    restoring.current = true;
    setEdgeDraft(restored.edgeDraft);
    setEnrollmentDraft(restored.enrollmentDraft);
    setEdgeVersion(restored.edgeVersion);
    setEnrollmentVersion(restored.enrollmentVersion);
    restoring.current = false;
    retain();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [controller, restored]);
  const reset = () => {
    controller.cancelReview();
    setEdgeDraft(undefined);
    setEnrollmentDraft(undefined);
    setEdgeVersion("");
    setEnrollmentVersion("");
    setError("");
  };
  const patch = (
    update: (value: SecurityRegistrationPolicy) => SecurityRegistrationPolicy,
  ) => {
    controller.cancelReview();
    setError("");
    try {
      if (!enrollmentDraft)
        setEnrollmentVersion(currentSecurityVersionBinding(state.version));
      setEnrollmentDraft(
        update(registrationDraft(enrollmentDraft ?? state.registrationPolicy)),
      );
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : "Unsupported enrollment policy.",
      );
    }
  };
  return {
    apply: async () => {
      await controller.apply();
      if (controller.getSnapshot().mutation !== "applied") return;
      reset();
      await controller.loadPolicies();
    },
    error,
    edgeMode: edgeDraft ?? state.edgePolicy?.mode,
    edgeDirty: edgeDraft !== undefined,
    enrollment: enrollmentDraft ?? state.registrationPolicy,
    enrollmentDirty: enrollmentDraft !== undefined,
    choices: enrollmentChoices(state.policyIssuers),
    load: (cursor = "") => {
      if (!cursor) reset();
      else controller.cancelReview();
      void controller.loadPolicies(cursor);
    },
    cancel: reset,
    setEdgeMode: (mode: EdgeEndpointMode) => {
      controller.cancelReview();
      if (edgeDraft === undefined)
        setEdgeVersion(currentSecurityVersionBinding(state.version));
      setEdgeDraft(mode);
    },
    setEnabled: (enabled: boolean) => patch((value) => ({ ...value, enabled })),
    toggleProfile: (profile: SecurityRegistrationProfile, enabled: boolean) =>
      patch((value) => ({
        ...value,
        profiles: enabled
          ? [
              ...value.profiles.filter(
                (item) =>
                  enrollmentProfileKey(item) !== enrollmentProfileKey(profile),
              ),
              profile,
            ]
          : value.profiles.filter(
              (item) =>
                enrollmentProfileKey(item) !== enrollmentProfileKey(profile),
            ),
      })),
    reviewChanges: () => {
      setError("");
      const version = currentSecurityVersionBinding(state.version);
      if (
        (edgeDraft !== undefined && edgeVersion !== version) ||
        (enrollmentDraft !== undefined && enrollmentVersion !== version)
      ) {
        setError(
          "The inspected policy changed. Reload shared settings before reviewing the draft.",
        );
        return;
      }
      const changes: SecurityChange[] = [];
      const labels: string[] = [];
      if (edgeDraft !== undefined) {
        const mode = EDGE_MODES.find(([mode]) => mode === edgeDraft);
        if (!mode) {
          setError("Choose one supported tail → head mode.");
          return;
        }
        changes.push(
          change({
            case: "putEdgePolicy",
            value: {
              $typeName: "graph.v1.SecurityEdgePolicy",
              version: 1,
              mode: edgeDraft,
            },
          }),
        );
        labels.push(`Shared Edge editing: ${mode[1]}`);
      }
      if (enrollmentDraft) {
        if (enrollmentDraft.enabled && !enrollmentDraft.profiles.length) {
          setError(
            "Select at least one eligible profile before enabling registration.",
          );
          return;
        }
        if (enrollmentDraft.enabled && !state.joiningRole) {
          setError(
            "personal_user is missing. Ask an administrator to create its scoped Role through a reviewed change before enabling registration.",
          );
          return;
        }
        changes.push(
          change({ case: "putRegistrationPolicy", value: enrollmentDraft }),
        );
        labels.push(
          `${enrollmentDraft.enabled ? "Enable" : "Disable"} new-user registration; joining Role personal_user; existing users retain login`,
        );
      }
      if (changes.length) controller.review(labels.join(". "), changes);
    },
  };
}
