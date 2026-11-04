import { Button, Checkbox, Title3 } from "@fluentui/react-components";
import {
  LogPolicyAdoptionState,
  OperationalGroup,
  OperationalSettingsApplicationState,
  OperationalSettingsChangeMode,
  OperationalSettingsError,
  OperationalSettingsSource,
  type OperationalGroupSettings,
  type OperationalBackupRetentionPreview,
  type OperationalCapacityPreview,
} from "lantern-sdk/web";
import {
  operationalDefinition,
  operationalDraft,
  type OperationalDraft,
} from "~/lib/client/usecase/settings/operational-settings-fields";
import { OperationalSettingField } from "./OperationalSettingField";
import styles from "./OperationalPolicyCard.module.css";
export type OperationalPolicyCardProps = {
  settings: OperationalGroupSettings;
  selected: boolean;
  draft?: OperationalDraft;
  blocked: boolean;
  retentionRequired: boolean;
  retentionAcknowledged: boolean;
  backupPreview?: OperationalBackupRetentionPreview;
  capacityPreview?: OperationalCapacityPreview;
  previewBackup(): void;
  previewCapacity(): void;
  select(adopt?: boolean): void;
  edit(key: string, value: string): void;
  acknowledge(value: boolean): void;
  review(): void;
};
export function OperationalPolicyCard(props: OperationalPolicyCardProps) {
  const { settings, selected, draft, blocked } = props,
    definition = operationalDefinition(settings.group);
  const effective = settings.effective
      ? operationalDraft(settings.effective)
      : undefined,
    desired = settings.desired?.values
      ? operationalDraft(settings.desired.values)
      : undefined;
  const modeLabels = {
    [OperationalSettingsChangeMode.UNAVAILABLE]: "Unavailable",
    [OperationalSettingsChangeMode.HOT]: "Applies to new work",
    [OperationalSettingsChangeMode.WORKER_RESTART]:
      "Applies at worker transition",
    [OperationalSettingsChangeMode.PROCESS_RESTART]: "Process restart required",
    [OperationalSettingsChangeMode.COORDINATED_RESTART]:
      "Coordinated restart required",
  };
  const sources = {
    [OperationalSettingsSource.DEFAULT]: "Default",
    [OperationalSettingsSource.ENVIRONMENT]: "Environment",
    [OperationalSettingsSource.DERIVED]: "Derived startup value",
    [OperationalSettingsSource.MANAGED]: "Managed policy",
  };
  const applications = {
    [OperationalSettingsApplicationState.UNAVAILABLE]: "Owner unavailable",
    [OperationalSettingsApplicationState.APPLIED]: settings.desired
      ? "Applied on this node"
      : "Startup values active",
    [OperationalSettingsApplicationState.PENDING]: "Pending on this node",
    [OperationalSettingsApplicationState.ERROR]:
      "Application error on this node",
  };
  return (
    <section className={styles.card} aria-label={definition.label}>
      <Title3 as="h3">{definition.label}</Title3>
      <p>{definition.note}</p>
      <dl className={styles.status}>
        <dt>Ownership</dt>
        <dd>
          {settings.adoption === LogPolicyAdoptionState.MANAGED
            ? "Managed policy"
            : settings.adoption === LogPolicyAdoptionState.FENCED_UNADOPTED
              ? "Not adopted; format floor retained"
              : "Not adopted"}
        </dd>
        <dt>Application</dt>
        <dd>
          {applications[settings.application as keyof typeof applications]}
        </dd>
        <dt>Change mode</dt>
        <dd>{modeLabels[settings.changeMode as keyof typeof modeLabels]}</dd>
        <dt>Desired / applied generation</dt>
        <dd>
          {settings.desired?.generation.toString() ?? "0"} /{" "}
          {settings.appliedGeneration.toString()}
        </dd>
      </dl>
      {(settings.effectiveCompatibilityFingerprint ||
        settings.desired?.compatibilityFingerprint) && (
        <p className={styles.reference}>
          Effective Search fingerprint:{" "}
          {settings.effectiveCompatibilityFingerprint || "Unavailable"}. Desired
          fingerprint:{" "}
          {settings.desired?.compatibilityFingerprint || "Not adopted"}.
        </p>
      )}
      {settings.restartRequired && (
        <p role="status">
          The desired policy is committed. This node still requires the reported
          restart procedure.
        </p>
      )}
      {settings.error !== OperationalSettingsError.NONE && (
        <p role="status">
          {settings.error === OperationalSettingsError.OWNER_UNAVAILABLE
            ? "The runtime owner is unavailable. Editing is disabled."
            : `Application error: ${OperationalSettingsError[settings.error].toLowerCase().replaceAll("_", " ")}.`}
        </p>
      )}
      {settings.preparation && (
        <p className={styles.reference}>
          Retained preparation{" "}
          {settings.preparation.changeId?.namespace.toString()}:
          {Array.from(settings.preparation.changeId?.nonce ?? [], (b) =>
            b.toString(16).padStart(2, "0"),
          ).join("")}
          . Check the retained original operation for its outcome.
        </p>
      )}
      {(effective ||
        desired ||
        settings.error === OperationalSettingsError.INVALID_POLICY) && (
        <div className={styles.tableWrap}>
          <table>
            <caption>Values on this node</caption>
            <thead>
              <tr>
                <th scope="col">Setting</th>
                <th scope="col">Effective</th>
                <th scope="col">Desired</th>
                <th scope="col">Effective source</th>
              </tr>
            </thead>
            <tbody>
              {definition.fields.map((field, index) => (
                <tr key={field.key}>
                  <th scope="row">{field.label}</th>
                  <td>
                    {effective?.[field.key] ??
                      (settings.error ===
                      OperationalSettingsError.INVALID_POLICY
                        ? "Unknown (invalid startup)"
                        : "Unavailable")}
                  </td>
                  <td>{desired?.[field.key] ?? "Not adopted"}</td>
                  <td>
                    {sources[
                      settings.provenance[index]?.source as keyof typeof sources
                    ] || "Unavailable"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!effective &&
        settings.error === OperationalSettingsError.INVALID_POLICY && (
          <p>
            Startup values are invalid and cannot be represented as effective
            typed values. Enter every field of a valid policy explicitly; no
            fallback value has been substituted.
          </p>
        )}
      {!selected && (
        <Button
          disabled={
            blocked ||
            (!effective &&
              settings.error !== OperationalSettingsError.INVALID_POLICY)
          }
          onClick={() => props.select()}
        >
          Edit {definition.label.toLowerCase()}
        </Button>
      )}
      {selected && !draft && (
        <div>
          <p>
            Explicit adoption makes this group authoritative in sys. Startup
            environment values will no longer override it.
          </p>
          <Button
            disabled={
              blocked ||
              (!effective &&
                settings.error !== OperationalSettingsError.INVALID_POLICY)
            }
            onClick={() => props.select(true)}
          >
            Use this node’s values for adoption
          </Button>
        </div>
      )}
      {selected && draft && (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            props.review();
          }}
        >
          <div className={styles.fields}>
            {definition.fields.map((field) => (
              <OperationalSettingField
                key={field.key}
                field={field}
                value={draft[field.key] ?? ""}
                disabled={blocked}
                change={(value) => props.edit(field.key, value)}
              />
            ))}
          </div>
          {settings.group === OperationalGroup.CAPACITY && (
            <section aria-label="Connected-node capacity preview">
              <Button disabled={blocked} onClick={props.previewCapacity}>
                Preview connected-node capacity
              </Button>
              {props.capacityPreview && (
                <>
                  <p>
                    Member {props.capacityPreview.member}; observed owner
                    generation{" "}
                    {props.capacityPreview.effectiveGeneration.toString()}.
                    Counters are sampled separately and can change. Admission
                    footprints include retained Put barriers. This local sample
                    does not predict admission or cluster-wide capacity.
                  </p>
                  <div className={styles.tableWrap}>
                    <table>
                      <caption>Capacity usage sample</caption>
                      <thead>
                        <tr>
                          <th scope="col">Counter</th>
                          <th scope="col">Usage</th>
                          <th scope="col">Over proposed cap</th>
                        </tr>
                      </thead>
                      <tbody>
                        {(
                          [
                            [
                              "Vertex admission footprint",
                              props.capacityPreview.vertexFootprint,
                              props.capacityPreview.verticesOverLimit,
                            ],
                            [
                              "Edge admission footprint",
                              props.capacityPreview.edgeFootprint,
                              props.capacityPreview.edgesOverLimit,
                            ],
                            [
                              "Vertex causal identities",
                              props.capacityPreview.vertexCausalEntries,
                              props.capacityPreview.vertexCausalOverLimit,
                            ],
                            [
                              "Edge causal identities",
                              props.capacityPreview.edgeCausalEntries,
                              props.capacityPreview.edgeCausalOverLimit,
                            ],
                          ] as const
                        ).map(([label, count, over]) => (
                          <tr key={String(label)}>
                            <th scope="row">{label}</th>
                            <td>{count.toString()}</td>
                            <td>{over ? "Yes" : "No"}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </>
              )}
            </section>
          )}
          {props.retentionRequired && (
            <section aria-label="Connected-node retention preview">
              <p>
                Preview samples this connected node only. The catalog can change
                before future scheduled production; this does not predict
                cluster-wide deletions.
              </p>
              <Button disabled={blocked} onClick={props.previewBackup}>
                Preview connected-node retention
              </Button>
              {props.backupPreview && (
                <p>
                  {props.backupPreview.complete
                    ? "Complete sample"
                    : "Incomplete sample"}
                  : {props.backupPreview.validBackups.toString()} valid backups
                  ({props.backupPreview.validBytes.toString()} bytes);{" "}
                  {props.backupPreview.pruneBackups.toString()} removal
                  candidates ({props.backupPreview.pruneBytes.toString()}{" "}
                  bytes). Informational sample fingerprint:{" "}
                  {props.backupPreview.fingerprint || "unavailable"}.
                </p>
              )}
            </section>
          )}
          {props.retentionRequired && (
            <Checkbox
              checked={props.retentionAcknowledged}
              disabled={blocked || !props.backupPreview?.complete}
              label="I acknowledge that future retention may remove older backups; Review and Apply do not delete them."
              onChange={(_, data) => props.acknowledge(data.checked === true)}
            />
          )}
          <Button
            appearance="primary"
            type="submit"
            disabled={
              blocked ||
              (props.retentionRequired &&
                (!props.retentionAcknowledged ||
                  !props.backupPreview?.complete))
            }
          >
            Review {definition.label.toLowerCase()} change
          </Button>
        </form>
      )}
    </section>
  );
}
