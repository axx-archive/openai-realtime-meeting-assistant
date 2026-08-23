import type { ScoutMessage } from '../api/types';
import { workHasDecisionCard } from './workPresentation';
import { workActivityThreadRef } from './workTimeline';

export const workActivityDismissalLedgerVersion = 1 as const;
export const maxWorkActivityDismissals = 20;

export type WorkActivityDismissalRecord = {
  workKey: string;
  versionKey: string;
  dismissedAt: number;
};

export type WorkActivityDismissalLedger = {
  version: typeof workActivityDismissalLedgerVersion;
  records: WorkActivityDismissalRecord[];
};

export type WorkActivitySurfaceIdentity = {
  workKey: string;
  versionKey: string;
};

const emptyLedger = (): WorkActivityDismissalLedger => ({
  version: workActivityDismissalLedgerVersion,
  records: [],
});

function stableKey(value: string): string {
  // Two independently-seeded FNV-1a passes keep persisted keys compact enough
  // for SecureStore without retaining account emails, prompts, or artifact IDs.
  let left = 0x811c9dc5;
  let right = 0x9e3779b9;
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    left ^= code;
    left = Math.imul(left, 0x01000193);
    right ^= code + index;
    right = Math.imul(right, 0x85ebca6b);
  }
  return `${(left >>> 0).toString(36)}${(right >>> 0).toString(36)}`;
}

function normalizedStatus(status: unknown): string {
  return String(status ?? '').trim().toLowerCase();
}

function semanticWorkState(message: ScoutMessage): string {
  const work = workActivityThreadRef(message);
  const status = normalizedStatus(work?.status);
  if (['complete', 'completed', 'published'].includes(status)) return 'delivered';
  if (['error', 'failed', 'needs_attention', 'rejected', 'blocked'].includes(status)) return 'attention';
  if (['approval_required', 'needs_input', 'parked'].includes(status) && workHasDecisionCard(work)) return 'decision';
  return 'active';
}

/**
 * A status pill is one viewer's transient reminder, not shared conversation
 * state. Progress and internal stage churn deliberately do not change this
 * identity; a new run, decision, terminal state, result, or review state does.
 */
export function workActivitySurfaceIdentity(
  threadId: string,
  message: ScoutMessage | null | undefined,
): WorkActivitySurfaceIdentity | null {
  const work = workActivityThreadRef(message);
  if (!work) return null;
  const owner = String(work.artifactId ?? work.id ?? message?.id ?? '').trim();
  if (!owner) return null;
  const workKey = stableKey(`${String(threadId).trim()}\u0000${owner}`);
  const checkpointId = workHasDecisionCard(work)
    ? String(work.checkpoint?.id ?? '').trim()
    : '';
  const version = [
    semanticWorkState(message as ScoutMessage),
    checkpointId,
    String(work.resultArtifactId ?? '').trim(),
    String(work.resultQualityState ?? '').trim().toLowerCase(),
    String(work.resultApprovalState ?? '').trim().toLowerCase(),
    String(work.followUpStatus ?? '').trim().toLowerCase(),
    String(work.attentionReason ?? '').trim().toLowerCase(),
  ].join('\u0000');
  return { workKey, versionKey: stableKey(version) };
}

export function workActivityDismissalStorageKey(viewerEmail: string): string {
  const viewer = String(viewerEmail).trim().toLowerCase();
  return `bonfire.workActivityDismissals.v1.${stableKey(viewer)}`;
}

export function parseWorkActivityDismissalLedger(raw: unknown): WorkActivityDismissalLedger {
  if (typeof raw !== 'string' || !raw.trim()) return emptyLedger();
  try {
    const parsed = JSON.parse(raw) as Partial<WorkActivityDismissalLedger>;
    if (parsed.version !== workActivityDismissalLedgerVersion || !Array.isArray(parsed.records)) return emptyLedger();
    const seen = new Set<string>();
    const records: WorkActivityDismissalRecord[] = [];
    for (const candidate of parsed.records) {
      const workKey = String(candidate?.workKey ?? '').trim();
      const versionKey = String(candidate?.versionKey ?? '').trim();
      const dismissedAt = Number(candidate?.dismissedAt ?? 0);
      if (!workKey || !versionKey || !Number.isFinite(dismissedAt) || dismissedAt <= 0 || seen.has(workKey)) continue;
      seen.add(workKey);
      records.push({ workKey, versionKey, dismissedAt });
      if (records.length === maxWorkActivityDismissals) break;
    }
    return { version: workActivityDismissalLedgerVersion, records };
  } catch {
    return emptyLedger();
  }
}

export function workActivityIsDismissed(
  ledger: WorkActivityDismissalLedger,
  identity: WorkActivitySurfaceIdentity,
): boolean {
  return ledger.records.some((record) => (
    record.workKey === identity.workKey && record.versionKey === identity.versionKey
  ));
}

export function recordWorkActivityDismissal(
  ledger: WorkActivityDismissalLedger,
  identity: WorkActivitySurfaceIdentity,
  dismissedAt = Date.now(),
): WorkActivityDismissalLedger {
  const next = ledger.records
    .filter((record) => record.workKey !== identity.workKey)
    .sort((left, right) => right.dismissedAt - left.dismissedAt);
  next.unshift({ ...identity, dismissedAt });
  return {
    version: workActivityDismissalLedgerVersion,
    records: next.slice(0, maxWorkActivityDismissals),
  };
}
