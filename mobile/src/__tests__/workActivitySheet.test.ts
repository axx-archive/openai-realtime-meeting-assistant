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

test('native Activity sheet presents four quiet customer phases for decks and documents', () => {
  assert.deepEqual(
    workActivityPhaseStates(packagingMessage('running', 'layout_plan')),
    ['complete', 'complete', 'current', 'upcoming'],
  );
  assert.deepEqual(
    workActivityPhaseStates(packagingMessage('complete', 'ship_approval')),
    ['complete', 'complete', 'complete', 'complete'],
  );
  const document = packagingMessage('running', 'draft_render');
  document.thread = { ...document.thread!, processId: 'document_report' };
  assert.deepEqual(workActivityPhaseStates(document), ['complete', 'complete', 'current', 'upcoming']);
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
      resultArtifactVersion: 7,
      resultArtifactDigest: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
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

test('generic governed completion opens from Activity without becoming timeline media', () => {
  const governed: ScoutMessage = {
    id: 'governed-result',
    kind: 'work_result',
    role: 'scout',
    createdAt: '2026-08-22T12:00:00Z',
    work: {
      id: 'record-1',
      runId: 'run-1',
      title: 'Completed work',
      status: 'complete',
      workerName: 'Scout',
      currentStage: 'done',
      summary: 'Ready.',
      artifactId: 'artifact-1',
      artifactHref: '/api/stride/v1/work/runs/run-1/artifact',
      evidenceHref: '',
      providerExecutionFenced: false,
    },
  };
  assert.deepEqual(workActivityPhaseStates(governed), []);
  assert.deepEqual(workActivityResultPresentation(governed), {
    kind: 'work',
    state: 'open',
    title: 'Work complete',
    body: 'The completed work is available here and in Files.',
    actionLabel: 'Open completed work',
  });
});
