import type { ApiClient } from '../client';
import { ApiRequestError } from '../client';
import type { DraftInput, ThreadAction } from '../types';

/** One durable, replayable user action captured while offline. */
export type OutboxAction =
  | { kind: 'thread_action'; threadId: string; action: ThreadAction }
  | { kind: 'thread_snooze'; threadId: string; until: string }
  | { kind: 'thread_reminder'; threadId: string; remindAt: string | null }
  | { kind: 'thread_open'; threadId: string }
  /** draftId may be a local id (`local-…`) for drafts created offline. */
  | { kind: 'draft_save'; draftId: string; accountId: string; input: DraftInput }
  | { kind: 'draft_send'; draftId: string };

export type OutboxEntryStatus = 'queued' | 'conflict' | 'failed';

export interface OutboxEntry {
  id: string;
  seq: number; // replay order
  createdAt: string;
  attempts: number;
  status: OutboxEntryStatus;
  lastError: string | null;
  action: OutboxAction;
  /**
   * Principal user id the action was queued under (X-Calendium-Act-As
   * delegation), or null when queued as the signed-in user themselves.
   * Optional because entries persisted before this field existed have no tag;
   * a missing tag is treated as null (self) everywhere.
   */
  actingAs?: string | null;
}

export interface OutboxStorage {
  load(): Promise<OutboxEntry[]>;
  save(entries: OutboxEntry[]): Promise<void>;
}

export interface ReplayReport {
  replayed: OutboxEntry[];
  conflicts: OutboxEntry[];
  failed: OutboxEntry[];
  /** True when replay stopped early (network/auth/5xx backoff) with entries still queued. */
  interrupted: boolean;
}

export const LOCAL_DRAFT_PREFIX = 'local-';
export const isLocalDraftId = (id: string): boolean => id.startsWith(LOCAL_DRAFT_PREFIX);

/** Pairs of actions that cancel each other out when both are still queued. */
const CANCELLING: Partial<Record<ThreadAction, ThreadAction>> = {
  star: 'unstar',
  unstar: 'star',
  read: 'unread',
  unread: 'read',
  archive: 'move_to_inbox',
  move_to_inbox: 'archive',
};

const MAX_ATTEMPTS = 5;
const MAX_ENTRIES = 500;

export class Outbox {
  private entries: OutboxEntry[] = [];
  private seq = 0;
  private loaded = false;
  private replaying = false;
  private readonly listeners = new Set<(entries: readonly OutboxEntry[]) => void>();

  constructor(
    private readonly storage: OutboxStorage,
    private readonly newId: () => string = () => crypto.randomUUID()
  ) {}

  async init(): Promise<void> {
    this.entries = await this.storage.load();
    this.seq = this.entries.reduce((m, e) => Math.max(m, e.seq), 0) + 1;
    this.loaded = true;
    this.notify();
  }

  get pending(): readonly OutboxEntry[] {
    return this.entries;
  }

  get queuedCount(): number {
    return this.entries.filter((e) => e.status === 'queued').length;
  }

  subscribe(fn: (entries: readonly OutboxEntry[]) => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /**
   * Queue an action, coalescing against what's already queued:
   * - a thread_action cancelled by an opposite queued one removes BOTH
   *   (star→unstar while offline = nothing to replay);
   * - duplicate thread_actions are dropped;
   * - snooze / reminder / open / draft_save replace the queued entry for the
   *   same target (keeping the original seq so a later queued send still
   *   replays after the save it depends on).
   * Returns the stored entry, or null when the action coalesced away.
   *
   * `actingAs` tags the entry with the delegation identity in effect (null =
   * self). Coalescing/dedupe/replace never cross identities: a star queued as
   * self and an unstar queued while acting are mutations of DIFFERENT
   * accounts and must both survive.
   */
  async enqueue(
    action: OutboxAction,
    opts?: { actingAs?: string | null }
  ): Promise<OutboxEntry | null> {
    const actingAs = opts?.actingAs ?? null;
    const queued = this.entries.filter(
      (e) => e.status === 'queued' && (e.actingAs ?? null) === actingAs
    );

    if (action.kind === 'thread_action') {
      const cancel = CANCELLING[action.action];
      const opposite = queued.find(
        (e) =>
          e.action.kind === 'thread_action' &&
          e.action.threadId === action.threadId &&
          e.action.action === cancel
      );
      if (opposite) {
        this.entries = this.entries.filter((e) => e.id !== opposite.id);
        await this.persist();
        return null;
      }
      const dup = queued.find(
        (e) =>
          e.action.kind === 'thread_action' &&
          e.action.threadId === action.threadId &&
          e.action.action === action.action
      );
      if (dup) return dup;
    }

    const replaceable = queued.find((e) => this.sameTarget(e.action, action));
    if (replaceable) {
      replaceable.action = action;
      replaceable.createdAt = new Date().toISOString();
      await this.persist();
      return replaceable;
    }

    const entry: OutboxEntry = {
      id: this.newId(),
      seq: this.seq++,
      createdAt: new Date().toISOString(),
      attempts: 0,
      status: 'queued',
      lastError: null,
      action,
      actingAs,
    };
    this.entries.push(entry);
    if (this.entries.length > MAX_ENTRIES) {
      // Shed non-queued (conflict/failed) history first, then the oldest.
      const shed = this.entries.find((e) => e.status !== 'queued') ?? this.entries[0];
      if (shed) this.entries = this.entries.filter((e) => e.id !== shed.id);
    }
    await this.persist();
    return entry;
  }

  /**
   * Drop queued save/send entries for a draft (undo of a queued send). When
   * the send was queued together with a reply reminder (compose's offline
   * pairing), pass that thread's id so the queued thread_reminder is undone
   * with it — mirroring what an online send-undo rolls back.
   */
  async removeForDraft(draftId: string, pairedReminderThreadId?: string): Promise<void> {
    this.entries = this.entries.filter((e) => {
      if (e.status !== 'queued') return true;
      if (
        (e.action.kind === 'draft_save' || e.action.kind === 'draft_send') &&
        e.action.draftId === draftId
      ) {
        return false;
      }
      if (
        pairedReminderThreadId !== undefined &&
        e.action.kind === 'thread_reminder' &&
        e.action.threadId === pairedReminderThreadId
      ) {
        return false;
      }
      return true;
    });
    await this.persist();
  }

  /**
   * Sign-out: drop every entry (any status) and persist the empty queue so
   * nothing queued by the previous user can replay for — or leak to — the
   * next sign-in on this device.
   */
  async clear(): Promise<void> {
    this.entries = [];
    await this.persist();
  }

  /** Clear a surfaced conflict/failed entry once the user acknowledged it. */
  async dismiss(entryId: string): Promise<void> {
    this.entries = this.entries.filter((e) => e.id !== entryId);
    await this.persist();
  }

  /**
   * Replay queued entries in seq order. Stops (leaving entries queued) on
   * network errors, 401/402/429, and 5xx (after bumping attempts); marks
   * unrecoverable 4xx entries `conflict` and continues. Concurrent-safe:
   * a second call while replaying returns an empty report.
   *
   * Only entries tagged with `actingAs` (null/missing = self, the default)
   * are replayed — the given client must carry exactly that identity, so an
   * entry queued as self never replays through an acting client and vice
   * versa. Entries for other identities stay queued untouched; they replay
   * when the user returns to that identity.
   */
  async replay(client: ApiClient, opts?: { actingAs?: string | null }): Promise<ReplayReport> {
    const actingAs = opts?.actingAs ?? null;
    const report: ReplayReport = { replayed: [], conflicts: [], failed: [], interrupted: false };
    if (this.replaying || !this.loaded) return report;
    this.replaying = true;
    try {
      for (const entry of [...this.entries].sort((a, b) => a.seq - b.seq)) {
        if (entry.status !== 'queued') continue;
        if ((entry.actingAs ?? null) !== actingAs) continue;
        try {
          await this.execute(client, entry.action);
          this.entries = this.entries.filter((e) => e.id !== entry.id);
          report.replayed.push(entry);
        } catch (err) {
          if (err instanceof ApiRequestError) {
            if (err.status === 401 || err.status === 402 || err.status === 429) {
              report.interrupted = true;
              break;
            }
            if (err.status >= 500) {
              entry.attempts += 1;
              entry.lastError = err.message;
              if (entry.attempts >= MAX_ATTEMPTS) {
                entry.status = 'failed';
                report.failed.push(entry);
                continue;
              }
              report.interrupted = true;
              break;
            }
            // Remaining 4xx: server owns the truth — conflict, keep going.
            entry.status = 'conflict';
            entry.lastError = err.message;
            report.conflicts.push(entry);
            continue;
          }
          report.interrupted = true; // network failure
          break;
        }
      }
    } finally {
      this.replaying = false;
      await this.persist();
    }
    return report;
  }

  private async execute(client: ApiClient, action: OutboxAction): Promise<void> {
    switch (action.kind) {
      case 'thread_action':
        await client.actOnThread(action.threadId, action.action);
        return;
      case 'thread_snooze':
        await client.snoozeThread(action.threadId, action.until);
        return;
      case 'thread_reminder':
        await client.setThreadReminder(action.threadId, action.remindAt);
        return;
      case 'thread_open':
        await client.markThreadOpened(action.threadId);
        return;
      case 'draft_save': {
        if (isLocalDraftId(action.draftId)) {
          const created = await client.saveDraft({ ...action.input, accountId: action.accountId });
          this.rewriteDraftId(action.draftId, created.id);
        } else {
          await client.updateDraft(action.draftId, action.input);
        }
        return;
      }
      case 'draft_send':
        await client.sendDraft(action.draftId);
        return;
    }
  }

  /** After an offline-created draft gets a server id, repoint queued entries. */
  private rewriteDraftId(localId: string, serverId: string): void {
    for (const e of this.entries) {
      if (
        (e.action.kind === 'draft_save' || e.action.kind === 'draft_send') &&
        e.action.draftId === localId
      ) {
        e.action = { ...e.action, draftId: serverId };
      }
    }
  }

  private sameTarget(a: OutboxAction, b: OutboxAction): boolean {
    if (a.kind !== b.kind) return false;
    switch (a.kind) {
      case 'thread_snooze':
      case 'thread_reminder':
      case 'thread_open':
        return a.threadId === (b as typeof a).threadId;
      case 'draft_save':
        return a.draftId === (b as typeof a).draftId;
      default:
        return false; // thread_action handled above; draft_send never replaces
    }
  }

  private async persist(): Promise<void> {
    await this.storage.save(this.entries);
    this.notify();
  }

  private notify(): void {
    for (const fn of this.listeners) fn(this.entries);
  }
}
