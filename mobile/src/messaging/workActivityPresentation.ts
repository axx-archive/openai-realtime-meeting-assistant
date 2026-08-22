import type { ScoutMessage } from '../api/types';
import { packagingStudioCustomerPhases, packagingStudioPhase } from './workPresentation';
import { workMessageHasPrimaryResult } from './workTimeline';

export type WorkActivityPhaseState = 'complete' | 'current' | 'upcoming';

export type WorkActivityResultPresentation = {
  kind: 'presentation' | 'document';
  state: 'open' | 'review_required' | 'desktop_only';
  title: string;
  body: string;
  actionLabel?: string;
};

export function workActivityPhaseStates(message: ScoutMessage | null): WorkActivityPhaseState[] {
  if (!message?.thread || !String(message.thread.processId ?? '').startsWith('packaging_studio')) return [];
  const status = String(message.thread.status ?? '').trim().toLowerCase();
  if (['complete', 'completed', 'published'].includes(status)) {
    return packagingStudioCustomerPhases.map(() => 'complete');
  }
  const current = packagingStudioPhase(message.thread);
  const currentIndex = current ? Math.max(0, current.number - 1) : 0;
  return packagingStudioCustomerPhases.map((_phase, index) => (
    index < currentIndex ? 'complete' : index === currentIndex ? 'current' : 'upcoming'
  ));
}

/**
 * Projects the exact current result capability. Managed presentations are
 * actionable only when this revision is admitted and explicitly presentable;
 * stale drafts never inherit an older revision's Open action.
 */
export function workActivityResultPresentation(
  message: ScoutMessage | null,
): WorkActivityResultPresentation | null {
  if (!workMessageHasPrimaryResult(message) || !message?.thread) return null;
  const work = message.thread;
  const explicitResultId = String(work.resultArtifactId ?? '').trim();
  const resultType = String(work.resultArtifactType ?? work.mode ?? '').trim().toLowerCase();
  const kind = /^(?:html_deck|deck|presentation|slides?)$/u.test(resultType)
    ? 'presentation'
    : 'document';
  const qualityState = String(work.resultQualityState ?? '').trim().toLowerCase();
  const managed = Boolean(explicitResultId)
    && (String(work.mode ?? '').trim().toLowerCase() === 'goal' || Boolean(qualityState));

  if (managed && qualityState !== 'admitted') {
    return {
      kind,
      state: 'review_required',
      title: kind === 'presentation' ? 'Presentation needs review' : 'Document needs review',
      body: work.resultCanEdit === true
        ? 'Continue editing on desktop, then run a fresh review before sharing this version.'
        : 'Scout needs to finish the current review before this version can be opened.',
    };
  }

  if (kind === 'presentation' && managed && work.resultCanPresent !== true) {
    return {
      kind,
      state: 'desktop_only',
      title: 'Presentation unavailable on mobile',
      body: work.resultCanEdit === true
        ? 'Open Stride on desktop to review this version and its sharing state.'
        : 'This revision is not currently cleared for presentation.',
    };
  }

  if (kind === 'presentation' && !managed && work.resultCanPresent === false) {
    return {
      kind,
      state: 'desktop_only',
      title: 'Presentation unavailable on mobile',
      body: 'This presentation is not currently available in the native viewer.',
    };
  }

  return {
    kind,
    state: 'open',
    title: kind === 'presentation' ? 'Presentation ready' : 'Document ready',
    body: kind === 'presentation'
      ? 'The reviewed presentation is ready to open.'
      : 'The finished document is ready to open.',
    actionLabel: `Open ${kind}`,
  };
}
