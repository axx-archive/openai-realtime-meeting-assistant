import type { ScoutMessage, ScoutWorkThreadRef } from '../api/types';
import { workHasDecisionCard } from './workPresentation';

const authoredResultKinds = /^(?:html_deck|deck|presentation|slides?|markdown|document|doc|memo|brief|research|deep_research|report|analysis|table|data_table|spreadsheet|ideation|ideas|brainstorm)$/u;
const decisionStatuses = new Set(['approval_required', 'needs_input', 'parked']);
const historicalResultKindByProcessId: Readonly<Record<string, string>> = {
  packaging_studio: 'html_deck',
  document_report: 'markdown',
};
const governedWorkArtifactHref = /^\/api\/stride\/v1\/work\/runs\/[a-z0-9_-]+\/artifact$/u;

/**
 * Returns the server-declared result kind, or a compatibility kind derived
 * only from a closed, server-owned process contract. Historical goal receipts
 * can predate resultArtifactType; never guess from user copy, titles, or IDs.
 */
export function workResultArtifactKind(work: ScoutWorkThreadRef | null | undefined): string {
  const declaredKind = String(work?.resultArtifactType ?? '').trim().toLowerCase();
  if (declaredKind) return authoredResultKinds.test(declaredKind) ? declaredKind : '';

  const processId = String(work?.processId ?? '').trim().toLowerCase();
  return historicalResultKindByProcessId[processId] ?? '';
}

export function isScoutWorkMessage(message: ScoutMessage | null | undefined): boolean {
  const kind = String(message?.kind ?? '').trim().toLowerCase();
  return (kind === 'thread' || kind === 'artifact') && Boolean(message?.thread);
}

function isGovernedWorkMessage(message: ScoutMessage | null | undefined): boolean {
  const kind = String(message?.kind ?? '').trim().toLowerCase();
  return (kind === 'work_result' || kind === 'work_record') && Boolean(message?.work);
}

function governedWorkHasRichResult(message: ScoutMessage | null | undefined): boolean {
  if (!isGovernedWorkMessage(message)) return false;
  const status = String(message?.work?.status ?? '').trim().toLowerCase();
  const href = String(message?.work?.artifactHref ?? '').trim();
  return ['complete', 'completed', 'published'].includes(status)
    && governedWorkArtifactHref.test(href);
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
  const resultKind = workResultArtifactKind(work);
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
    const key = workMessageHasPrimaryResult(message)
      ? primaryResultKey(message.thread)
      : governedWorkHasRichResult(message)
        ? String(message.work?.artifactHref ?? '').trim()
        : '';
    if (key) latestResultIndex.set(key, index);
  });

  return messages.filter((message, index) => {
    const kind = String(message.kind ?? '').trim().toLowerCase();
    if (isGovernedWorkMessage(message)) {
      if (!governedWorkHasRichResult(message)) return false;
      const key = String(message.work?.artifactHref ?? '').trim();
      return Boolean(key) && latestResultIndex.get(key) === index;
    }
    // A proposed action is a real decision. Once accepted or dismissed, its
    // lifecycle belongs in Activity rather than becoming launch narration in
    // the conversation.
    if (kind === 'proposal' && message.proposal) {
      return !String(message.proposal.status ?? '').trim();
    }
    if (!isScoutWorkMessage(message)) return true;
    if (workMessageHasActionableDecision(message)) return true;
    if (workMessageHasPrimaryResult(message)) {
      const key = primaryResultKey(message.thread);
      return Boolean(key) && latestResultIndex.get(key) === index;
    }
    // A generic work reference is activity, even when it reaches a terminal
    // state. The server projects a real final artifact through the explicit
    // result fields above, which renders as rich media. Keeping a generic
    // terminal card as a substitute is what produced the repeated “Scout ·
    // Work” wall in channels. Historical/untyped references remain available
    // from the viewer-local Activity surface and Files, never as chat spam.
    return false;
  });
}

export function latestScoutWorkMessage(messages: readonly ScoutMessage[]): ScoutMessage | null {
  let fallback: ScoutMessage | null = null;
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!isScoutWorkMessage(message)) continue;
    fallback ??= message;
    if (workMessageHasPrimaryResult(message) || workMessageHasActionableDecision(message)) return message;
    const mode = String(message.thread?.mode ?? '').trim().toLowerCase();
    if (mode === 'goal') return message;
    const delegatedBy = String(message.thread?.delegatedBy ?? '').trim();
    const status = String(message.thread?.status ?? '').trim().toLowerCase();
    const terminal = ['complete', 'completed', 'published'].includes(status);
    // A direct active/failed run may not have a final result yet. It can own
    // the pill; a completed sub-agent/stage card cannot outrank its root.
    if (!delegatedBy && !terminal) return message;
  }
  return fallback;
}
