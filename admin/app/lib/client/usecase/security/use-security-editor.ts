import { useEffect, useRef } from "react";
import { useSecurityDraftField } from "./use-security-draft-field";
import type { SecurityEditorIntent } from "./security-draft-recovery";
import {
  SecurityAction,
  SecurityPermission,
  SecurityResource,
  SecurityPrincipalState,
  currentSecurityVersionBinding,
  type SecurityIssuer,
  type SecurityRole,
  type SecurityUser,
} from "lantern-sdk/web";
import type { SecurityManagementController } from "./security-management";
import {
  assignmentChange,
  buildIssuer,
  buildRole,
  change,
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
  const fields = useRef<Record<string, unknown>>({});
  const restoring = useRef(false);
  const retain = () => {
    if (restoring.current) return;
    const draft = fields.current as {
      mode:
        | SecurityEditorIntent["mode"]
        | "list"
        | "details"
        | "members"
        | "explain";
      selectedKind: SecurityEditorIntent["selectedKind"];
      selectedVersion: string;
      savedIssuer?: SecurityIssuer;
      savedRole?: SecurityRole;
      savedUser?: SecurityUser;
      issuer: IssuerDraft;
      role: RoleDraft;
      user: UserDraft;
      templateName: string;
      roleSource: SecurityEditorIntent["roleSource"];
    };
    if (draft.mode !== "create" && draft.mode !== "edit") {
      controller.retainEditorIntent(undefined);
      return;
    }
    const {
      locked: _issuerLocked,
      existing: _issuerExisting,
      hasSecretBinding: _secretBinding,
      nativeClients: _nativeClients,
      ...issuer
    } = draft.issuer;
    const {
      locked: _roleLocked,
      existing: _roleExisting,
      ...role
    } = draft.role;
    const {
      existing: _userExisting,
      assignments: _assignments,
      ...user
    } = draft.user;
    controller.retainEditorIntent({
      mode: draft.mode,
      selectedKind: draft.selectedKind,
      selectedVersion: draft.selectedVersion,
      issuerKey: draft.savedIssuer?.issuer,
      roleKey: draft.savedRole?.id,
      userKey: draft.savedUser?.identity,
      issuer,
      role,
      user,
      templateName: draft.templateName,
      roleSource: draft.roleSource,
    });
  };
  const [selectedVersion, setSelectedVersion] = useSecurityDraftField(
    "selectedVersion",
    fields,
    retain,
    "",
  );
  const [mode, setMode] = useSecurityDraftField<
    "list" | "details" | "create" | "edit" | "members" | "explain"
  >("mode", fields, retain, "list");
  const [selectedKind, setSelectedKind] = useSecurityDraftField<
    "issuers" | "users" | "roles"
  >("selectedKind", fields, retain, "roles");
  const [savedIssuer, setSavedIssuer] = useSecurityDraftField<SecurityIssuer>(
    "savedIssuer",
    fields,
    retain,
  );
  const [savedRole, setSavedRole] = useSecurityDraftField<SecurityRole>(
    "savedRole",
    fields,
    retain,
  );
  const [savedUser, setSavedUser] = useSecurityDraftField<SecurityUser>(
    "savedUser",
    fields,
    retain,
  );
  const [templateName, setTemplateName] = useSecurityDraftField(
    "templateName",
    fields,
    retain,
    "",
  );
  const [roleSource, setRoleSource] = useSecurityDraftField<
    "blank" | "template"
  >("roleSource", fields, retain, "blank");
  const [issuer, setIssuer] = useSecurityDraftField(
    "issuer",
    fields,
    retain,
    issuerDraft,
  );
  const [role, setRole] = useSecurityDraftField(
    "role",
    fields,
    retain,
    roleDraft,
  );
  const [user, setUser] = useSecurityDraftField(
    "user",
    fields,
    retain,
    userDraft,
  );
  const [error, setError] = useSecurityDraftField("error", fields, retain, "");
  const [templatePrefix, setTemplatePrefix] = useSecurityDraftField(
    "templatePrefix",
    fields,
    retain,
    "",
  );
  const [explanation, setExplanation] = useSecurityDraftField<ExplanationDraft>(
    "explanation",
    fields,
    retain,
    {
      issuer: "",
      subject: "",
      permission: SecurityPermission.READ,
      resourceKind: SecurityResource.DATA,
      key: "",
      edge: false,
      tail: "",
      head: "",
    },
  );
  const restored = controller.getSnapshot().restoredIntent?.editor;
  const selection = controller.getSnapshot().restoredSelection;
  useEffect(() => {
    if (!restored) return;
    restoring.current = true;
    try {
      setSavedIssuer(selection?.issuer);
      setSavedRole(selection?.role);
      setSavedUser(selection?.user);
      setIssuer({
        ...issuerDraft(selection?.issuer),
        ...restored.issuer,
        ...(selection?.issuer ? { issuer: selection.issuer.issuer } : {}),
      });
      setRole({
        ...roleDraft(selection?.role),
        ...restored.role,
        ...(selection?.role ? { id: selection.role.id } : {}),
      });
      setUser(
        selection?.user
          ? {
              ...userDraft(selection.user),
              state: restored.user.state,
              roleId: restored.user.roleId,
            }
          : { ...userDraft(), ...restored.user },
      );
      setSelectedKind(restored.selectedKind);
      setSelectedVersion(restored.selectedVersion);
      setTemplateName(restored.templateName);
      setRoleSource(restored.roleSource);
      setMode(restored.mode);
    } finally {
      restoring.current = false;
    }
    retain();
    // Restoration runs once for this validated controller metadata object.
    // Setters retain synchronous edits; their identity is intentionally local.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [controller, restored, selection]);
  const captureVersion = () => {
    const version = controller.getSnapshot().version;
    setSelectedVersion(version ? currentSecurityVersionBinding(version) : "");
  };
  const requireSelectedVersion = () => {
    const version = controller.getSnapshot().version;
    if (!version || selectedVersion !== currentSecurityVersionBinding(version))
      throw new Error(
        "The inspected policy changed. Reload and select the saved record again before reviewing this draft.",
      );
  };
  const run = (operation: () => void) => {
    setError("");
    try {
      operation();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Invalid draft.");
    }
  };
  return {
    apply: async () => {
      const reviewed = controller.getSnapshot().review;
      await controller.apply();
      if (controller.getSnapshot().mutation !== "applied") return;
      const operation = reviewed?.changes[0]?.operation;
      if (operation?.case === "putRole") {
        const saved = await controller.readSavedRole(operation.value.id);
        setSavedRole(saved ?? operation.value);
        setRole(roleDraft(saved ?? operation.value));
        setSelectedKind("roles");
        captureVersion();
        setMode("details");
      } else if (
        operation?.case === "putUser" ||
        operation?.case === "putAssignment" ||
        operation?.case === "deleteAssignment"
      ) {
        const identity = operation.value.identity;
        if (identity) {
          const saved = await controller.readSavedUser(identity);
          if (saved) {
            setSavedUser(saved);
            setUser(userDraft(saved));
            setSelectedKind("users");
            captureVersion();
            setMode("details");
          }
        }
      } else if (operation?.case === "putIssuer") {
        setSavedIssuer(operation.value);
        setIssuer(issuerDraft(operation.value));
        setMode("details");
        await controller.load();
      }
    },
    mode,
    selectedKind,
    savedIssuer,
    savedRole,
    savedUser,
    templateName,
    roleSource,
    setRoleSource: (source: "blank" | "template") => {
      controller.cancelReview();
      setRoleSource(source);
      setTemplateName("");
      setRole(roleDraft());
    },
    beginEdit: () => {
      controller.cancelReview();
      setMode("edit");
    },
    showMembers: () => {
      controller.cancelReview();
      setMode("members");
    },
    showExplanation: () => {
      controller.cancelReview();
      setMode("explain");
    },
    cancel: () => {
      controller.cancelReview();
      setError("");
      setIssuer(issuerDraft(savedIssuer));
      setRole(roleDraft(savedRole));
      setUser(userDraft(savedUser));
      setMode(savedIssuer || savedRole || savedUser ? "details" : "list");
    },
    close: () => {
      controller.cancelReview();
      setMode("list");
      setError("");
    },
    issuer,
    role,
    user,
    error,
    fieldError: (label: string) =>
      error.toLowerCase().includes(label.toLowerCase()) ? error : undefined,
    templatePrefix,
    setTemplatePrefix: (prefix: string) => {
      controller.cancelReview();
      controller.resetTemplates();
      setTemplatePrefix(prefix);
    },
    explanation,
    editIssuer: (value?: SecurityIssuer) => {
      controller.cancelReview();
      setError("");
      setSavedIssuer(value);
      captureVersion();
      setSavedRole(undefined);
      setSavedUser(undefined);
      setSelectedKind("issuers");
      setMode(value ? "details" : "create");
      setIssuer(issuerDraft(value));
    },
    editRole: (value?: SecurityRole) => {
      controller.cancelReview();
      setError("");
      run(() => {
        setRole(roleDraft(value));
        captureVersion();
        setSavedRole(value);
        setSavedIssuer(undefined);
        setSavedUser(undefined);
        setTemplateName("");
        setRoleSource("blank");
        setSelectedKind("roles");
        setMode(value ? "details" : "create");
      });
    },
    useTemplate: (value: SecurityRole) => {
      controller.cancelReview();
      setError("");
      setTemplateName(value.name);
      setMode("create");
      run(() =>
        setRole({
          ...roleDraft({ ...value, envOwned: false }),
          id: "",
          name: value.name,
          existing: false,
        }),
      );
    },
    editUser: (value?: SecurityUser) => {
      controller.cancelReview();
      setError("");
      setSavedUser(value);
      captureVersion();
      setSavedIssuer(undefined);
      setSavedRole(undefined);
      setSelectedKind("users");
      setMode(value ? "details" : "create");
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
      run(() => {
        requireSelectedVersion();
        controller.review(`Save Issuer ${issuer.issuer}`, [
          change({ case: "putIssuer", value: buildIssuer(issuer) }),
        ]);
      }),
    validateIssuer: () =>
      run(() => {
        void controller.validateIssuer(buildIssuer(issuer));
      }),
    disableIssuer: () =>
      run(() => {
        requireSelectedVersion();
        if (issuer.locked || !issuer.existing)
          throw new Error("Choose a mutable Issuer.");
        controller.review(
          `Disable Issuer ${issuer.issuer}; revoke its sessions`,
          [change({ case: "disableIssuer", value: issuer.issuer })],
        );
      }),
    deleteIssuer: () =>
      run(() => {
        requireSelectedVersion();
        if (issuer.locked || !issuer.existing)
          throw new Error("Choose a mutable Issuer.");
        controller.review(
          `Delete Issuer ${issuer.issuer}; revoke its sessions`,
          [change({ case: "deleteIssuer", value: issuer.issuer })],
        );
      }),
    saveRole: () =>
      run(() => {
        requireSelectedVersion();
        const value = buildRole(role);
        const changes = [change({ case: "putRole", value })];
        const label = `Save Role ${role.id}; affects every assigned user`;
        if (role.existing) controller.review(label, changes);
        else void controller.reviewNewRole(label, changes, value.id);
      }),
    deleteRole: () =>
      run(() => {
        requireSelectedVersion();
        buildRole(role);
        controller.review(
          `Delete Role ${role.id}; Server rejects remaining assignments or protected access`,
          [change({ case: "deleteRole", value: role.id })],
        );
      }),
    saveUser: () =>
      run(() => {
        requireSelectedVersion();
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
      run(() => {
        requireSelectedVersion();
        controller.review(
          `Delete ${user.subject || user.machineName}; remove memberships and revoke sessions`,
          [change({ case: "deleteUser", value: userIdentity(user) })],
        );
      }),
    revokeUser: () =>
      run(() => {
        requireSelectedVersion();
        controller.review(
          `Revoke sessions for ${user.subject || user.machineName}`,
          [change({ case: "revokeUserSessions", value: userIdentity(user) })],
        );
      }),
    assignRole: () =>
      run(() => {
        requireSelectedVersion();
        controller.review(
          `Assign ${user.roleId} to ${user.subject || user.machineName}`,
          [assignmentChange(user)],
        );
      }),
    removeAssignment: (roleId: string) =>
      run(() => {
        requireSelectedVersion();
        controller.review(
          `Remove ${roleId} from ${user.subject || user.machineName}`,
          [assignmentChange(user, true, roleId)],
        );
      }),
    explain: () =>
      run(() => {
        const identity = userIdentity({
          ...userDraft(),
          issuer: explanation.issuer,
          subject: explanation.subject,
        });
        void controller.explain(
          identity,
          explanation.edge
            ? explanation.permission === SecurityPermission.READ
              ? SecurityAction.EDGE_READ
              : SecurityAction.EDGE_WRITE
            : SecurityAction.UNSPECIFIED,
          explanation.resourceKind === SecurityResource.DATA &&
            !explanation.edge
            ? explanation.key
            : undefined,
          explanation.edge
            ? { tail: explanation.tail, head: explanation.head }
            : undefined,
          explanation.permission,
          explanation.resourceKind,
        );
      }),
  };
}
export type SecurityEditor = ReturnType<typeof useSecurityEditor>;
