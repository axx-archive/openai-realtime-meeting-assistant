import assert from 'node:assert/strict';
import test from 'node:test';

import type { ScoutMessage } from '../api/types';
import {
  workActivityPhaseStates,
  workActivityResultPresentation,
} from '../messaging/workActivityPresentation';

function packagingMessage(status: string, currentStage: string): ScoutMessage {
  return {
    id: 'activity',
    kind: 'thread',
    role: 'scout',
    createdAt: '2026-08-22T12:00:00Z',
    thread: {
      id: 'goal-1',
      mode: 'goal',
      processId: 'packaging_studio',
      query: 'Make the deck',
      status,
      currentStage,
      progressPercent: 64,
    },
  };
}

test('native Activity sheet presents five quiet customer stages instead of internal cards', () => {
  assert.deepEqual(
    workActivityPhaseStates(packagingMessage('running', 'layout_plan')),
    ['complete', 'complete', 'complete', 'current', 'upcoming'],
  );
  assert.deepEqual(
    workActivityPhaseStates(packagingMessage('complete', 'ship_approval')),
    ['complete', 'complete', 'complete', 'complete', 'complete'],
  );
  assert.deepEqual(workActivityPhaseStates({ ...packagingMessage('running', 'layout_plan'), thread: { ...packagingMessage('running', 'layout_plan').thread!, processId: 'research' } }), []);
});

test('Activity opens only the exact admitted presentation revision', () => {
  const base = packagingMessage('complete', 'ship_approval');
  const admitted: ScoutMessage = {
    ...base,
    thread: {
      ...base.thread!,
      resultArtifactId: 'deck-1',
      resultArtifactType: 'html_deck',
      resultQualityState: 'admitted',
      resultCanPresent: true,
      resultCanEdit: true,
    },
  };
  assert.deepEqual(workActivityResultPresentation(admitted), {
    kind: 'presentation',
    state: 'open',
    title: 'Presentation ready',
    body: 'The reviewed presentation is ready to open.',
    actionLabel: 'Open presentation',
  });

  const edited: ScoutMessage = {
    ...admitted,
    id: 'edited-deck',
    thread: { ...admitted.thread!, resultQualityState: 'edited_after_admission' },
  };
  assert.deepEqual(workActivityResultPresentation(edited), {
    kind: 'presentation',
    state: 'review_required',
    title: 'Presentation needs review',
    body: 'Continue editing on desktop, then run a fresh review before sharing this version.',
  });

  const fenced: ScoutMessage = {
    ...admitted,
    id: 'fenced-deck',
    thread: { ...admitted.thread!, resultCanPresent: false },
  };
  assert.deepEqual(workActivityResultPresentation(fenced), {
    kind: 'presentation',
    state: 'desktop_only',
    title: 'Presentation unavailable on mobile',
    body: 'Open Stride on desktop to review this version and its sharing state.',
  });
});
