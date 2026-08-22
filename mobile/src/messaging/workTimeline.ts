import type { ScoutMessage, ScoutWorkThreadRef } from '../api/types';
import { workHasDecisionCard } from './workPresentation';

const authoredResultKinds = /^(?:html_deck|deck|presentation|slides?|markdown|document|doc|memo|brief|research|deep_research|report|analysis|table|data_table|spreadsheet|ideation|ideas|brainstorm)$/u;
const decisionStatuses = new Set(['approval_required', 'needs_input', 'parked']);

export function isScoutWorkMessage(message: ScoutMessage | null | undefined): boolean {
  return String(message?.kind ?? '').trim().toLowerCase() === 'thread' && Boolean(message?.thread);
}

export function workMessageHasActionableDecision(message: ScoutMessage | null | undefined): boolean {
  if (!isScoutWorkMessage(message)) return false;
  const work = message?.thread;
  return decisionStatuses.has(String(work?.status ?? '').trim().toLowerCase())
    && workHasDecisionCard(work);
}

export function workMessageHasPrimaryResult(message: ScoutMessage | null | undefined): boolean {
  if (!isScoutWorkMessage(message)) return false;
  const work = message?.thread;
  const resultArtifactId = String(work?.resultArtifactId ?? '').trim();
  const resultKind = String(work?.resultArtifactType ?? '').trim().toLowerCase();
  if (resultArtifactId && authoredResultKinds.test(resultKind)) return true;

  // Legacy standalone presentations use the lifecycle artifact as the deck.
  // A goal's lifecycle artifact is never media and therefore never qualifies.
  const mode = String(work?.mode ?? '').trim().toLowerCase();
  const status = String(work?.status ?? '').trim().toLowerCase();
  return Boolean(String(work?.artifactId ?? '').trim())
    && ['complete', 'completed', 'published'].includes(status)
    && /^(?:html_deck|deck|presentation|slides?)$/u.test(mode);
}

function primaryResultKey(work: ScoutWorkThreadRef | undefined): string {
  return String(work?.resultArtifactId ?? work?.artifactId ?? '').trim();
}

/**
 * Process cards are activity, not conversation. Keep the latest exact authored
 * result and every real decision in the transcript; move all other work-stage
 * records into the one native Activity sheet.
 */
export function compactThreadWorkMessages(messages: readonly ScoutMessage[]): ScoutMessage[] {
  const latestResultIndex = new Map<string, number>();
  messages.forEach((message, index) => {
    if (!workMessageHasPrimaryResult(message)) return;
    const key = primaryResultKey(message.thread);
    if (key) latestResultIndex.set(key, index);
  });

  return messages.filter((message, index) => {
    if (!isScoutWorkMessage(message)) return true;
    if (workMessageHasActionableDecision(message)) return true;
    if (workMessageHasPrimaryResult(message)) {
      const key = primaryResultKey(message.thread);
      return Boolean(key) && latestResultIndex.get(key) === index;
    }
    const status = String(message.thread?.status ?? '').trim().toLowerCase();
    if (['queued', 'running', 'approval_required', 'needs_input', 'parked'].includes(status)) return false;
    const processId = String(message.thread?.processId ?? '').trim();
    const mode = String(message.thread?.mode ?? '').trim().toLowerCase();
    // Goal/process stage records live in Activity. Standalone terminal work
    // (for example a direct Research run) remains a real deliverable card.
    return !processId && mode !== 'goal';
  });
}

export function latestScoutWorkMessage(messages: readonly ScoutMessage[]): ScoutMessage | null {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    if (isScoutWorkMessage(messages[index])) return messages[index];
  }
  return null;
}
