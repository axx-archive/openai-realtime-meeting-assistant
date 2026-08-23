import type { ScoutMessage, ScoutWorkThreadRef } from '../api/types';
import { workHasDecisionCard } from './workPresentation';

const authoredResultKinds = /^(?:html_deck|deck|presentation|slides?|markdown|document|doc|memo|brief|research|deep_research|report|analysis|pdf|image|table|data_table|spreadsheet|workbook|bundle|file|ideation|ideas|brainstorm)$/u;
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

export function isGovernedWorkMessage(message: ScoutMessage | null | undefined): boolean {
  const kind = String(message?.kind ?? '').trim().toLowerCase();
  return (kind === 'work_result' || kind === 'work_record') && Boolean(message?.work);
}

/**
 * Governed Work receipts are not automatically customer deliverables. Admit a
 * center-timeline result only when the server supplied an exact authored kind;
 * a generic artifact endpoint alone is Activity/Files truth, not rich media.
 */
export function governedWorkResultArtifactKind(message: ScoutMessage | null | undefined): string {
  if (!isGovernedWorkMessage(message)) return '';
  const work = message?.work;
  for (const candidate of [work?.resultArtifactType, work?.artifactKind, work?.workKind, work?.outputKind]) {
    const kind = String(candidate ?? '').trim().toLowerCase();
    if (authoredResultKinds.test(kind)) return kind;
  }
  return '';
}

function validResultAssets(work: ScoutWorkThreadRef | null | undefined) {
  return (Array.isArray(work?.resultAssets) ? work.resultAssets : []).filter((asset) => (
    /^[0-9a-f]{64}$/u.test(String(asset?.ref ?? '').trim().toLowerCase())
    && String(asset?.kind ?? '').trim().toLowerCase() !== 'page_image'
  ));
}

/** A structured kind is feed media only with its closed server envelope. */
export function workRefHasClosedResultEnvelope(work: ScoutWorkThreadRef | null | undefined): boolean {
  const kind = workResultArtifactKind(work);
  const resultArtifactId = String(work?.resultArtifactId ?? '').trim();
  const resultArtifactVersion = Number(work?.resultArtifactVersion ?? 0);
  const resultArtifactDigest = String(work?.resultArtifactDigest ?? '').trim().toLowerCase();
  if (!resultArtifactId || !Number.isSafeInteger(resultArtifactVersion) || resultArtifactVersion < 1 || !/^[0-9a-f]{64}$/u.test(resultArtifactDigest)) return false;
  const assets = validResultAssets(work);
  if (/^(?:html_deck|deck|presentation|slides?|markdown|document|doc|memo|brief|research|deep_research|report|analysis|ideation|ideas|brainstorm)$/u.test(kind)) {
    return true;
  }
  if (kind === 'image') return assets.some((asset) => String(asset.kind ?? '').toLowerCase() === 'image' && String(asset.mime ?? '').toLowerCase().startsWith('image/'));
  if (kind === 'pdf') return assets.some((asset) => ['pdf', 'export'].includes(String(asset.kind ?? '').toLowerCase()) && String(asset.mime ?? '').toLowerCase() === 'application/pdf');
  if (/^(?:table|data_table|spreadsheet)$/u.test(kind)) {
    return Boolean(Array.isArray(work?.resultTable?.columns) && work.resultTable.columns.length && Array.isArray(work.resultTable.rows));
  }
  if (kind === 'workbook') {
    const mime = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet';
    const fileName = String(work?.resultWorkbook?.fileName ?? '').trim();
    return String(work?.resultWorkbook?.mime ?? '').trim().toLowerCase() === mime
      && /\.xlsx$/iu.test(fileName)
      && assets.some((asset) => String(asset.kind ?? '').toLowerCase() === 'export'
        && String(asset.mime ?? '').trim().toLowerCase() === mime
        && /\.xlsx$/iu.test(String(asset.name ?? '').trim())
        && String(asset.name ?? '').trim().toLowerCase() === fileName.toLowerCase());
  }
  if (kind === 'bundle' || kind === 'file') return assets.length > 0;
  return false;
}

export function governedWorkHasRichResult(message: ScoutMessage | null | undefined): boolean {
  if (!isGovernedWorkMessage(message)) return false;
  const status = String(message?.work?.status ?? '').trim().toLowerCase();
  const href = String(message?.work?.artifactHref ?? '').trim();
  const work = message?.work;
  const ref: ScoutWorkThreadRef = {
    id: String(work?.runId || work?.id || ''), mode: 'work', query: String(work?.title || ''), status,
    resultArtifactId: work?.resultArtifactId,
    resultArtifactType: work?.resultArtifactType,
    resultArtifactVersion: work?.resultArtifactVersion,
    resultArtifactDigest: work?.resultArtifactDigest,
    resultAssets: work?.resultAssets,
    resultTable: work?.resultTable,
    resultWorkbook: work?.resultWorkbook,
  };
  return ['complete', 'completed', 'published'].includes(status)
    && governedWorkArtifactHref.test(href)
    && Boolean(String(work?.resultArtifactId ?? '').trim())
    && workRefHasClosedResultEnvelope(ref);
}

export function governedWorkArtifactAvailable(message: ScoutMessage | null | undefined): boolean {
  if (!isGovernedWorkMessage(message)) return false;
  const status = String(message?.work?.status ?? '').trim().toLowerCase();
  const href = String(message?.work?.artifactHref ?? '').trim();
  return ['complete', 'completed', 'published'].includes(status)
    && governedWorkArtifactHref.test(href);
}

/**
 * Activity consumes one stable presentation shape for both authored threads
 * and generic governed receipts. The original message remains the action
 * target, so opening a governed receipt still uses its authenticated route.
 */
export function workActivityThreadRef(
  message: ScoutMessage | null | undefined,
): ScoutWorkThreadRef | null {
  if (isScoutWorkMessage(message)) return message?.thread ?? null;
  if (!isGovernedWorkMessage(message) || !message?.work) return null;
  const work = message.work;
  return {
    id: String(work.runId || work.id),
	rootRunId: String(work.rootRunId || '').trim() || undefined,
	parentRunId: String(work.parentRunId || '').trim() || undefined,
    mode: 'work',
    outputFamily: work.outputFamily || 'Work',
    query: work.title,
    status: work.status,
    artifactId: work.artifactId,
    agentName: work.workerName,
    currentStage: work.currentStage,
    progressPercent: work.progressPercent,
    progressNote: work.summary,
	resultArtifactId: work.resultArtifactId,
	resultArtifactType: work.resultArtifactType || governedWorkResultArtifactKind(message) || undefined,
	resultArtifactVersion: work.resultArtifactVersion,
	resultArtifactDigest: work.resultArtifactDigest,
	resultAssets: work.resultAssets,
	resultTable: work.resultTable,
	resultWorkbook: work.resultWorkbook,
    resultTitle: work.title,
    resultPreview: work.summary,
  };
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
  if (resultArtifactId && authoredResultKinds.test(resultKind) && workRefHasClosedResultEnvelope(work)) return true;

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
	const candidates = messages
		.map((message) => ({ message, work: workActivityThreadRef(message) }))
		.filter((candidate): candidate is { message: ScoutMessage; work: ScoutWorkThreadRef } => Boolean(candidate.work?.id));
	const latest = candidates[candidates.length - 1];
	if (!latest) return null;
	const parentRunId = String(latest.work.parentRunId ?? '').trim();
	const rootRunId = String(latest.work.rootRunId ?? '').trim();
	if (!parentRunId || !rootRunId) return latest.message;
	let root: (typeof candidates)[number] | undefined;
	for (let index = candidates.length - 1; index >= 0; index -= 1) {
		const candidate = candidates[index];
		if (String(candidate?.work.id ?? '').trim() === rootRunId
			&& !String(candidate?.work.parentRunId ?? '').trim()) {
			root = candidate;
			break;
		}
	}
	if (!root) return latest.message;
	// Root ownership and child supersession are both explicit. Never use an
	// agent display name, delegatedBy copy, or an older result as topology.
	return {
		...root.message,
		thread: {
			...root.work,
			status: latest.work.status,
			currentStage: latest.work.currentStage || root.work.currentStage,
			progressPercent: latest.work.progressPercent,
			progressNote: latest.work.progressNote || root.work.progressNote,
		},
	};
}
