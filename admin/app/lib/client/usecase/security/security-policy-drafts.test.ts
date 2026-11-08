import { expect, test } from "bun:test";
import { RegistrationProfileKind } from "lantern-sdk/web";
import { buildIssuer, issuerDraft } from "./security-drafts";
import {
  enrollmentChoices,
  enrollmentProfileKey,
  registrationDraft,
} from "./security-policy-drafts";

test("enrollment starts OFF, and exact browser/native choices remain distinct", () => {
  expect(registrationDraft()).toMatchObject({
    version: 2,
    enabled: false,
    roleId: "personal_user",
    profiles: [],
  });
  const issuer = buildIssuer({
    ...issuerDraft(),
    issuer: "https://idp.example",
    clientId: "browser-client",
    redirectUri: "https://admin.example/auth/callback",
    enabled: true,
  });
  issuer.nativeClients = [
    {
      $typeName: "graph.v1.SecurityNativeClient",
      id: "browser",
      kind: 1,
      clientId: "native-client",
      serviceOrigin: "https://admin.example",
      presenters: [],
      redirectUri: "",
      androidPackage: "",
      hasSecretBinding: false,
    },
  ];
  const choices = enrollmentChoices([issuer]);
  expect(choices.map((profile) => profile.kind)).toEqual([
    RegistrationProfileKind.BROWSER,
    RegistrationProfileKind.NATIVE,
  ]);
  expect(new Set(choices.map(enrollmentProfileKey)).size).toBe(2);
  expect(enrollmentChoices([{ ...issuer, enabled: false }])).toEqual([]);
  expect(enrollmentChoices([{ ...issuer, deleted: true }])).toEqual([]);
});
test("registration drafts reject incompatible policy and preserve retained selections without mutating Server data", () => {
  const policy = {
    ...registrationDraft(),
    enabled: true,
    profiles: [
      {
        $typeName: "graph.v1.SecurityRegistrationProfile" as const,
        issuer: "https://other-idp.example",
        clientProfileId: "browser",
        kind: RegistrationProfileKind.BROWSER,
      },
    ],
  };
  const draft = registrationDraft(policy);
  draft.profiles[0].issuer = "https://changed.example";
  expect(policy.profiles[0].issuer).toBe("https://other-idp.example");
  expect(() => registrationDraft({ ...policy, version: 1 })).toThrow(
    "Unsupported",
  );
  expect(() => registrationDraft({ ...policy, roleId: "data_admin" })).toThrow(
    "Unsupported",
  );
});
