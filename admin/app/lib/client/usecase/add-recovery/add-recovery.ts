import {
  parseReceiptOperationContext,
  type EdgeInput,
  type ReceiptOperationContext,
} from "lantern-sdk/web";
import type { AddRecoveryGateway } from "~/lib/client/infrastructure/api/add-recovery";

export interface AddAttempt {
  id: string;
  actor: string;
  tail: string;
  head: string;
  phase: "sending" | "uncertain" | "confirmed" | "handledUndisclosed";
  receipt?: ReceiptOperationContext;
  message: string;
}
export interface AddRecoveryStorage {
  read(): string | null;
  write(value: string): void;
  newID(): string;
}
const limit = 20;
const uncertainMessage =
  "Add outcome is unknown. The original request is retained; no mutation will be resent.";

/** Per-tab non-credential metadata, saved before the first possible Add send. */
export class AddRecoveryStore {
  private attempts: readonly AddAttempt[] = [];
  private readonly listeners = new Set<() => void>();
  private invalid = false;
  constructor(private readonly storage: AddRecoveryStorage) {
    try {
      const raw = storage.read();
      if (raw === null) return;
      if (raw.length > 128 * 1024) throw new Error("Recovery record too large");
      const values: unknown = JSON.parse(raw);
      if (!Array.isArray(values) || values.length > limit)
        throw new Error("Invalid recovery record");
      this.attempts = values.map((value: unknown) => {
        if (typeof value !== "object" || value === null)
          throw new Error("Invalid recovery item");
        const item = value as Record<string, unknown>;
        if (
          typeof item.id !== "string" ||
          typeof item.actor !== "string" ||
          typeof item.tail !== "string" ||
          typeof item.head !== "string" ||
          !["sending", "uncertain", "confirmed", "handledUndisclosed"].includes(
            String(item.phase),
          )
        )
          throw new Error("Invalid recovery fields");
        const receipt =
          item.receipt === undefined
            ? undefined
            : parseReceiptOperationContext(item.receipt);
        if (
          receipt &&
          (receipt.operationIds?.length !== 1 ||
            !/^[a-f0-9]{98}$/.test(receipt.operationIds[0]!))
        )
          throw new Error("Invalid receipt identity");
        return {
          id: item.id,
          actor: item.actor,
          tail: item.tail,
          head: item.head,
          phase:
            item.phase === "sending"
              ? "uncertain"
              : (item.phase as AddAttempt["phase"]),
          receipt,
          message:
            item.phase === "confirmed"
              ? "Original Add confirmed."
              : uncertainMessage,
        };
      });
    } catch {
      // Losing or overwriting unparseable uncertainty would admit a fresh Add.
      this.invalid = true;
    }
  }
  getSnapshot = (): readonly AddAttempt[] => this.attempts;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private save(attempts: readonly AddAttempt[]) {
    const serialized = JSON.stringify(attempts);
    if (serialized.length > 128 * 1024)
      throw new Error("Retained Add history is full. No request was resent.");
    this.storage.write(serialized);
    this.attempts = attempts;
    this.listeners.forEach((listener) => listener());
  }
  pending(actor: string, tail: string, head: string): AddAttempt | undefined {
    return this.attempts.find(
      (a) =>
        a.actor === actor &&
        a.tail === tail &&
        a.head === head &&
        a.phase !== "confirmed",
    );
  }
  private update(id: string, patch: Partial<AddAttempt>) {
    this.save(this.attempts.map((a) => (a.id === id ? { ...a, ...patch } : a)));
  }
  async add(
    actor: string,
    input: EdgeInput,
    gateway: AddRecoveryGateway,
    signal?: AbortSignal,
  ) {
    if (this.invalid)
      throw new Error("Retained Add history cannot be read. No Add was sent.");
    if (this.pending(actor, input.tail, input.head))
      throw new Error(uncertainMessage);
    // Reserve capacity before asynchronous preparation; never evict uncertainty.
    const retained = this.attempts.filter((a) => a.phase !== "confirmed");
    if (retained.length >= limit)
      throw new Error("Too many retained Add requests. No Add was sent.");
    const placeholder: AddAttempt = {
      id: this.storage.newID(),
      actor,
      tail: input.tail,
      head: input.head,
      phase: "sending",
      message: "Preparing Add request.",
    };
    this.save([...retained, placeholder]);
    let prepared;
    try {
      prepared = await gateway.prepare(input, signal);
    } catch (error) {
      this.save(this.attempts.filter((a) => a.id !== placeholder.id));
      throw error;
    }
    this.update(placeholder.id, {
      receipt: prepared.receipt,
      message: "Add response is pending.",
    });
    try {
      const result = await gateway.send(prepared, signal);
      if (result.kind === "acceptedUndisclosed") {
        this.update(placeholder.id, {
          phase: "handledUndisclosed",
          message:
            "Add request handled. Its effect is undisclosed; do not resend.",
        });
      } else {
        this.update(placeholder.id, {
          phase: "confirmed",
          message: "Original Add confirmed.",
        });
      }
      return result;
    } catch (error) {
      if (gateway.definiteRefusal(error)) {
        this.save(this.attempts.filter((a) => a.id !== placeholder.id));
        throw error;
      }
      this.update(placeholder.id, {
        phase: "uncertain",
        message: uncertainMessage,
      });
      throw new Error(uncertainMessage, { cause: error });
    }
  }
  async check(
    id: string,
    actor: string,
    gateway: AddRecoveryGateway,
    signal?: AbortSignal,
  ) {
    const item = this.attempts.find((a) => a.id === id && a.actor === actor);
    if (!item || item.phase === "sending" || item.phase === "confirmed") return;
    if (!item.receipt) {
      this.update(id, {
        message:
          "This Add has no original-result receipt. Its outcome stays unknown. Reading the current edge cannot confirm it; do not resend.",
      });
      return;
    }
    try {
      const status = await gateway.status(item.receipt, signal);
      if (status.operationId !== item.receipt.operationIds[0])
        throw new Error("Mismatched Add status");
      if (status.state === "confirmed") {
        if (
          status.receipt.groupId !== item.receipt.groupId ||
          status.receipt.itemCount !== 1 ||
          status.receipt.itemIndex !== 0 ||
          status.receipt.originalResult.kind !== "addEdge"
        )
          throw new Error("Mismatched Add receipt");
        this.update(id, {
          phase: "confirmed",
          message:
            "Original Add confirmed by its retained receipt. No mutation was resent.",
        });
      } else if (status.state === "effectUndisclosed") {
        this.update(id, {
          phase: "handledUndisclosed",
          message:
            "The original effect is undisclosed. No confirmation or resend permission is available.",
        });
      } else {
        this.update(id, {
          phase: "uncertain",
          message:
            "Original Add cannot currently be proved. Its request identity is retained; do not resend.",
        });
      }
    } catch {
      this.update(id, {
        message:
          "Add status is unavailable. The original request is retained; no mutation was resent.",
      });
    }
  }
}
