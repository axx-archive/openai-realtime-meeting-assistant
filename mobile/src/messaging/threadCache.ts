import type { ScoutThread, ScoutThreadDetailResponse } from '../api/types';

// Private chat data remains process-local. This cache survives navigation and
// foreground refreshes, but is discarded on account change or app termination.
let activeScope = '';
let threadList: ScoutThread[] | null = null;
const threadDetails = new Map<string, ScoutThreadDetailResponse>();
const MAX_THREAD_DETAILS = 8;

function selectScope(scope: string) {
  const next = scope.trim().toLowerCase();
  if (next === activeScope) return next;
  activeScope = next;
  threadList = null;
  threadDetails.clear();
  return next;
}

export function readThreadListCache(scope: string) {
  if (!selectScope(scope)) return null;
  return threadList;
}

export function writeThreadListCache(scope: string, threads: ScoutThread[]) {
  if (!selectScope(scope)) return;
  threadList = threads;
}

export function readThreadDetailCache(scope: string, threadId: string) {
  if (!selectScope(scope)) return null;
  const cached = threadDetails.get(threadId) ?? null;
  if (cached) {
    threadDetails.delete(threadId);
    threadDetails.set(threadId, cached);
  }
  return cached;
}

export function writeThreadDetailCache(
  scope: string,
  threadId: string,
  response: ScoutThreadDetailResponse,
) {
  if (!selectScope(scope)) return;
  threadDetails.delete(threadId);
  threadDetails.set(threadId, response);
  while (threadDetails.size > MAX_THREAD_DETAILS) {
    const oldest = threadDetails.keys().next().value as string | undefined;
    if (!oldest) break;
    threadDetails.delete(oldest);
  }
}
