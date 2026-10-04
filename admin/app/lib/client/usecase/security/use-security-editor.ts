import { useState } from "react";
import {
  SecurityAction,
  SecurityPrincipalState,
  type SecurityIssuer,
  type SecurityRole,
  type SecurityUser,
} from "lantern-sdk/web";
import type { SecurityManagementController } from "./security-management";
import {
  EXPLANATION_ACTIONS,
  assignmentChange,
  buildIssuer,
  buildRole,
  change,
  globalAction,
  issuerDraft,
  newRule,
  roleDraft,
  userDraft,
  userIdentity,
  type ExplanationDraft,
  type IssuerDraft,
  type RoleDraft,
  type RuleDraft,
  type UserDraft,
} from "./security-drafts";

export function useSecurityEditor(controller: SecurityManagementController) {
  const [issuer, setIssuer] = useState(issuerDraft);
  const [role, setRole] = useState(roleDraft);
  const [user, setUser] = useState(userDraft);
  const [error, setError] = useState("");
  const [templatePrefix, setTemplatePrefix] = useState("");
  const [explanation, setExplanation] = useState<ExplanationDraft>({
    issuer: "",
    subject: "",
    action: SecurityAction.VERTEX_READ,
    key: "",
    edge: false,
    tail: "",
    head: "",
  });
  const run = (operation: () => void) => {
    setError("");
    try {
      operation();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Invalid draft.");
    }
  };
  return {
    issuer,
    role,
    user,
    error,
    templatePrefix,
    setTemplatePrefix,
    explanation,
    actions: EXPLANATION_ACTIONS,
    editIssuer: (value?: SecurityIssuer) => {
      controller.cancelReview();
      setError("");
      setIssuer(issuerDraft(value));
    },
    editRole: (value?: SecurityRole) => {
      controller.cancelReview();
      setError("");
      run(() => setRole(roleDraft(value)));
    },
    useTemplate: (value: SecurityRole) => {
      controller.cancelReview();
      setError("");
      run(() =>
        setRole({
          ...roleDraft(value),
          id: "",
          name: value.name,
          existing: false,
        }),
      );
    },
    editUser: (value?: SecurityUser) => {
      controller.cancelReview();
      setError("");
      setUser(userDraft(value));
    },
    patchIssuer: (patch: Partial<IssuerDraft>) => {
      controller.cancelReview();
      setIssuer((value) => ({ ...value, ...patch }));
    },
    patchRole: (patch: Partial<RoleDraft>) => {
      controller.cancelReview();
      setRole((value) => ({ ...value, ...patch }));
    },
    patchUser: (patch: Partial<UserDraft>) => {
      controller.cancelReview();
      setUser((value) => ({ ...value, ...patch }));
    },
    patchExplanation: (patch: Partial<ExplanationDraft>) =>
      setExplanation((value) => ({ ...value, ...patch })),
    addRule: () => {
      controller.cancelReview();
      setRole((value) => {
        let next = value.rules.length + 1;
        while (value.rules.some((rule) => rule.id === `rule-${next}`)) next++;
        return { ...value, rules: [...value.rules, newRule(next)] };
      });
    },
    patchRule: (index: number, patch: Partial<RuleDraft>) => {
      controller.cancelReview();
      setRole((value) => ({
        ...value,
        rules: value.rules.map((rule, i) =>
          i === index
            ? {
                ...rule,
                ...patch,
                global:
                  patch.action !== undefined
                    ? globalAction(patch.action)
                    : rule.global,
              }
            : rule,
        ),
      }));
    },
    removeRule: (index: number) => {
      controller.cancelReview();
      setRole((value) => ({
        ...value,
        rules: value.rules.filter((_, i) => i !== index),
      }));
    },
    saveIssuer: () =>
      run(() =>
        controller.review(`Save Issuer ${issuer.issuer}`, [
          change({ case: "putIssuer", value: buildIssuer(issuer) }),
        ]),
      ),
    validateIssuer: () =>
      run(() => {
        void controller.validateIssuer(buildIssuer(issuer));
      }),
    disableIssuer: () =>
      run(() => {
        if (issuer.locked || !issuer.existing)
          throw new Error("Choose a mutable Issuer.");
        controller.review(
          `Disable Issuer ${issuer.issuer}; revoke its sessions`,
          [change({ case: "disableIssuer", value: issuer.issuer })],
        );
      }),
    deleteIssuer: () =>
      run(() => {
        if (issuer.locked || !issuer.existing)
          throw new Error("Choose a mutable Issuer.");
        controller.review(
          `Delete Issuer ${issuer.issuer}; revoke its sessions`,
          [change({ case: "deleteIssuer", value: issuer.issuer })],
        );
      }),
    saveRole: () =>
      run(() =>
        controller.review(`Save Role ${role.id}; affects every assigned user`, [
          change({ case: "putRole", value: buildRole(role) }),
        ]),
      ),
    deleteRole: () =>
      run(() => {
        buildRole(role);
        controller.review(
          `Delete Role ${role.id}; Server rejects remaining assignments or protected access`,
          [change({ case: "deleteRole", value: role.id })],
        );
      }),
    saveUser: () =>
      run(() => {
        if (
          ![
            SecurityPrincipalState.ACTIVE,
            SecurityPrincipalState.SUSPENDED,
          ].includes(user.state)
        )
          throw new Error("Choose active or suspended.");
        controller.review(
          `${user.state === SecurityPrincipalState.ACTIVE ? "Activate" : "Suspend"} ${user.subject || user.machineName}`,
          [
            change({
              case: "putUser",
              value: {
                $typeName: "graph.v1.SecurityUserStateChange",
                identity: userIdentity(user),
                state: user.state,
              },
            }),
          ],
        );
      }),
    deleteUser: () =>
      run(() =>
        controller.review(
          `Delete ${user.subject || user.machineName}; remove memberships and revoke sessions`,
          [change({ case: "deleteUser", value: userIdentity(user) })],
        ),
      ),
    revokeUser: () =>
      run(() =>
        controller.review(
          `Revoke sessions for ${user.subject || user.machineName}`,
          [change({ case: "revokeUserSessions", value: userIdentity(user) })],
        ),
      ),
    assignRole: () =>
      run(() =>
        controller.review(
          `Assign ${user.roleId} to ${user.subject || user.machineName}`,
          [assignmentChange(user)],
        ),
      ),
    removeAssignment: (roleId: string) =>
      run(() =>
        controller.review(
          `Remove ${roleId} from ${user.subject || user.machineName}`,
          [assignmentChange(user, true, roleId)],
        ),
      ),
    explain: () =>
      run(() => {
        const identity = userIdentity({
          ...userDraft(),
          issuer: explanation.issuer,
          subject: explanation.subject,
        });
        void controller.explain(
          identity,
          explanation.action,
          globalAction(explanation.action) || explanation.edge
            ? undefined
            : explanation.key,
          explanation.edge
            ? { tail: explanation.tail, head: explanation.head }
            : undefined,
        );
      }),
  };
}
export type SecurityEditor = ReturnType<typeof useSecurityEditor>;
