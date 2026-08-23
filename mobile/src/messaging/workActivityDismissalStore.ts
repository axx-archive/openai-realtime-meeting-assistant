import * as SecureStore from 'expo-secure-store';

import {
  parseWorkActivityDismissalLedger,
  recordWorkActivityDismissal,
  workActivityDismissalStorageKey,
  workActivityIsDismissed,
  type WorkActivitySurfaceIdentity,
  type WorkActivityDismissalLedger,
} from './workActivityDismissal';

const memory = new Map<string, WorkActivityDismissalLedger>();
const hydration = new Map<string, Promise<WorkActivityDismissalLedger>>();

async function readLedger(viewerEmail: string): Promise<WorkActivityDismissalLedger> {
  const key = workActivityDismissalStorageKey(viewerEmail);
  const cached = memory.get(key);
  if (cached) return cached;
  const pending = hydration.get(key);
  if (pending) return pending;
  const read = SecureStore.getItemAsync(key)
    .then((raw) => parseWorkActivityDismissalLedger(raw))
    .catch(() => parseWorkActivityDismissalLedger(null))
    .then((ledger) => {
      memory.set(key, ledger);
      hydration.delete(key);
      return ledger;
    });
  hydration.set(key, read);
  return read;
}

export async function viewerDismissedWorkActivity(
  viewerEmail: string,
  identity: WorkActivitySurfaceIdentity,
): Promise<boolean> {
  if (!String(viewerEmail).trim()) return false;
  return workActivityIsDismissed(await readLedger(viewerEmail), identity);
}

export async function dismissWorkActivityForViewer(
  viewerEmail: string,
  identity: WorkActivitySurfaceIdentity,
): Promise<void> {
  if (!String(viewerEmail).trim()) return;
  const key = workActivityDismissalStorageKey(viewerEmail);
  const next = recordWorkActivityDismissal(await readLedger(viewerEmail), identity);
  // Optimistic local authority keeps dismissal immediate even if secure
  // storage is temporarily unavailable; this never mutates the shared thread.
  memory.set(key, next);
  try {
    await SecureStore.setItemAsync(key, JSON.stringify(next));
  } catch {
    // Installation persistence may fail in simulator/web edge cases. The
    // in-memory viewer preference remains valid for the current session.
  }
}

