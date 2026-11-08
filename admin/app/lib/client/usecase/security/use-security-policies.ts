import { useState } from "react";
import {
  EdgeEndpointMode,
  currentSecurityVersionBinding,
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
  const [edgeDraft, setEdgeDraft] = useState<EdgeEndpointMode>();
  const [enrollmentDraft, setEnrollmentDraft] =
    useState<SecurityRegistrationPolicy>();
  const [edgeVersion, setEdgeVersion] = useState("");
  const [enrollmentVersion, setEnrollmentVersion] = useState("");
  const [error, setError] = useState("");
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
    reviewEdge: () => {
      if (edgeVersion !== currentSecurityVersionBinding(state.version)) {
        setError(
          "The inspected policy changed. Reload shared settings before reviewing the draft.",
        );
        return;
      }
      if (!EDGE_MODES.some(([mode]) => mode === edgeDraft)) {
        setError("Choose one supported tail → head mode.");
        return;
      }
      controller.review(
        `Change shared Edge editing to ${EDGE_MODES.find(([mode]) => mode === edgeDraft)?.[1]}; affects every user and credential`,
        [
          change({
            case: "putEdgePolicy",
            value: {
              $typeName: "graph.v1.SecurityEdgePolicy",
              version: 1,
              mode: edgeDraft!,
            },
          }),
        ],
      );
    },
    reviewEnrollment: () => {
      if (!enrollmentDraft) return;
      if (enrollmentVersion !== currentSecurityVersionBinding(state.version)) {
        setError(
          "The inspected policy changed. Reload shared settings before reviewing the draft.",
        );
        return;
      }
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
      controller.review(
        `${enrollmentDraft.enabled ? "Enable" : "Disable"} new-user registration; joining Role personal_user; existing users retain login`,
        [change({ case: "putRegistrationPolicy", value: enrollmentDraft })],
      );
    },
  };
}
