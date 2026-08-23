import assert from 'node:assert/strict';
import test from 'node:test';

import type { ScoutMessage, ScoutWorkThreadRef } from '../api/types';
import {
  compactThreadWorkMessages,
  latestScoutWorkMessage,
  workMessageHasActionableDecision,
  workMessageHasPrimaryResult,
  workRefHasClosedResultEnvelope,
  workResultArtifactKind,
} from '../messaging/workTimeline';

const exactDigest = 'a'.repeat(64);

function message(id: string, thread?: ScoutMessage['thread']): ScoutMessage {
  return {
    id,
    kind: thread ? 'thread' : 'message',
    role: thread ? 'scout' : 'user',
    text: id,
    createdAt: `2026-08-22T12:00:${String(id.length).padStart(2, '0')}Z`,
    ...(thread ? { thread } : {}),
  };
}

test('timeline keeps conversation, real decisions, and only the latest exact authored result', () => {
  const process = (id: string, status = 'complete'): ScoutWorkThreadRef => ({
    id,
    mode: 'goal',
    processId: 'packaging_studio',
    query: 'Make the deck',
    status,
    artifactId: `goal-${id}`,
  });
  const messages = [
    message('request'),
    message('brief-card', process('brief')),
    message('research-card', process('research')),
    message('decision', {
      ...process('decision', 'needs_input'),
      checkpoint: {
        id: 'direction',
        stageId: 'story',
        question: 'Which audience should lead?',
        options: [{ id: 'buyers', label: 'Buyers', action: 'proceed' }],
      },
    }),
    message('deck-v1', {
      ...process('root'),
      resultArtifactId: 'deck-1',
      resultArtifactType: 'html_deck',
      resultArtifactVersion: 1,
      resultArtifactDigest: exactDigest,
      resultTitle: 'Field Notes',
    }),
    message('deck-v2', {
      ...process('root'),
      resultArtifactId: 'deck-1',
      resultArtifactType: 'html_deck',
      resultArtifactVersion: 2,
      resultArtifactDigest: 'b'.repeat(64),
      resultTitle: 'Field Notes',
    }),
  ];

  assert.deepEqual(
    compactThreadWorkMessages(messages).map(({ id }) => id),
    ['request', 'decision', 'deck-v2'],
  );
  assert.equal(workMessageHasActionableDecision(messages[3]), true);
  assert.equal(workMessageHasPrimaryResult(messages[4]), true);
  assert.equal(latestScoutWorkMessage(messages)?.id, 'deck-v2');
});

test('generic terminal cards join non-actionable stages in Activity while rich results remain in the timeline', () => {
  const hidden = message('hidden', {
    id: 'stage-1',
    mode: 'goal',
    query: 'Make the deck',
    status: 'needs_input',
    artifactId: 'goal-1',
  });
  const legacyDeck = message('legacy-deck', {
    id: 'legacy-1',
    mode: 'presentation',
    query: 'Make the deck',
    status: 'complete',
    artifactId: 'deck-legacy',
  });
  const directResearch = message('direct-research', {
    id: 'research-1',
    mode: 'research',
    query: 'Research the category',
    status: 'complete',
    artifactId: 'research-artifact',
  });
  const reportResult = message('report-result', {
    id: 'report-goal',
    mode: 'goal',
    processId: 'document_report',
    query: 'Write the report',
    status: 'complete',
    artifactId: 'report-goal-root',
    resultArtifactId: 'report-artifact',
    resultArtifactType: 'report',
    resultArtifactVersion: 1,
    resultArtifactDigest: exactDigest,
  });
  const richResearch = message('rich-research', {
    id: 'research-rich',
    mode: 'research',
    query: 'Research the category',
    status: 'complete',
    artifactId: 'research-rich-artifact',
    resultArtifactId: 'research-rich-artifact',
    resultArtifactType: 'research',
    resultArtifactVersion: 1,
    resultArtifactDigest: exactDigest,
  });
  assert.equal(workMessageHasActionableDecision(hidden), false);
  assert.deepEqual(
    compactThreadWorkMessages([hidden, legacyDeck, directResearch, reportResult, richResearch]).map(({ id }) => id),
    ['legacy-deck', 'report-result', 'rich-research'],
  );
});

test('generic complete, failed, and untyped work cards never substitute for final rich media', () => {
  const statuses = ['complete', 'failed', 'needs_attention'];
  const generic = statuses.map((status) => message(`generic-${status}`, {
    id: `run-${status}`,
    mode: 'work',
    query: 'Do the work',
    status,
    artifactId: `artifact-${status}`,
  }));
  const human = message('human-context');
  assert.deepEqual(
    compactThreadWorkMessages([human, ...generic]).map(({ id }) => id),
    ['human-context'],
  );
});

test('artifact stage receipts and generic governed completions stay out while exact typed governed results remain rich', () => {
  const stage = message('artifact-stage', {
    id: 'stage-artifact',
    mode: 'artifacts',
    query: 'Compile the deck',
    status: 'complete',
    artifactId: 'internal-stage-receipt',
  });
  stage.kind = 'artifact';
  const genericRecord: ScoutMessage = {
    id: 'generic-record',
    kind: 'work_record',
    role: 'scout',
    text: 'Internal work record',
    createdAt: '2026-08-22T12:01:00Z',
    work: {
      id: 'record-1', runId: 'run-1', title: 'Work', status: 'complete', workerName: 'Scout',
      currentStage: 'Done', summary: 'Internal receipt', progressPercent: 100,
      artifactId: 'artifact-1', artifactHref: '', evidenceHref: '', providerExecutionFenced: false,
    },
  };
  const result = (id: string, artifactKind?: string): ScoutMessage => ({
    ...genericRecord,
    id,
    kind: 'work_result',
    text: 'The report is ready.',
    work: {
      ...genericRecord.work!,
      title: 'Evidence brief',
      summary: 'The evidence-linked brief is ready.',
      artifactHref: '/api/stride/v1/work/runs/run-1/artifact',
      ...(artifactKind ? {
        artifactKind,
        resultArtifactId: 'report-artifact',
        resultArtifactType: artifactKind,
        resultArtifactVersion: 1,
        resultArtifactDigest: exactDigest,
      } : {}),
    },
  });
  const invalidResult = result('unsafe-result', 'report');
  invalidResult.work = { ...invalidResult.work!, artifactHref: 'https://attacker.example/result' };

  assert.deepEqual(
    compactThreadWorkMessages([
      stage,
      genericRecord,
      result('untyped-result'),
      result('result-v1', 'report'),
      invalidResult,
      result('result-v2', 'report'),
    ])
      .map(({ id }) => id),
    ['result-v2'],
  );
});

test('the compact Activity owner preserves generic governed completion outside the center timeline', () => {
  const governed: ScoutMessage = {
    id: 'governed-complete',
    kind: 'work_result',
    role: 'scout',
    createdAt: '2026-08-22T12:03:00Z',
    work: {
      id: 'record-2', runId: 'run-2', title: 'Completed work', status: 'complete', workerName: 'Scout',
      currentStage: 'Done', summary: 'The work is ready.',
      artifactId: 'artifact-2', artifactHref: '/api/stride/v1/work/runs/run-2/artifact',
      evidenceHref: '', providerExecutionFenced: false,
    },
  };
  assert.deepEqual(compactThreadWorkMessages([governed]), []);
  assert.equal(latestScoutWorkMessage([governed])?.id, governed.id);
});

test('only an unresolved proposal interrupts the timeline', () => {
  const proposal = (id: string, status?: string): ScoutMessage => ({
    id,
    kind: 'proposal',
    role: 'scout',
    text: 'Run the work?',
    createdAt: '2026-08-22T12:02:00Z',
    proposal: {
      id: `proposal-${id}`,
      kind: 'goal_run',
      summary: 'Build the presentation',
      status,
    },
  });
  assert.deepEqual(
    compactThreadWorkMessages([
      proposal('pending'),
      proposal('accepted', 'accepted'),
      proposal('dismissed', 'dismissed'),
    ]).map(({ id }) => id),
    ['pending'],
  );
});

test('persistent activity truth follows the newest run, never an older delivered result', () => {
  const delivered = message('older-delivered', {
    id: 'goal-old',
    mode: 'goal',
    processId: 'packaging_studio',
    query: 'Make the first deck',
    status: 'complete',
    artifactId: 'goal-old-root',
    resultArtifactId: 'deck-old',
    resultArtifactType: 'html_deck',
    resultArtifactVersion: 1,
    resultArtifactDigest: exactDigest,
    resultQualityState: 'admitted',
    resultCanPresent: true,
  });
  const failed = message('newer-failed', {
    id: 'goal-new',
    mode: 'goal',
    processId: 'packaging_studio',
    query: 'Revise the deck',
    status: 'failed',
    artifactId: 'goal-new-root',
    currentStage: 'quality_gate',
  });
  assert.equal(latestScoutWorkMessage([delivered, failed])?.id, 'newer-failed');

	const genericFailed = message('newer-generic-failed', {
		id: 'work-new',
		mode: 'work',
		query: 'Run a direct task',
		status: 'failed',
		artifactId: 'work-new-root',
	});
	assert.equal(latestScoutWorkMessage([delivered, genericFailed])?.id, 'newer-generic-failed');
});

test('the bottom Activity pill follows the customer work owner instead of a later generic stage card', () => {
  const root = message('deck-root', {
    id: 'goal-root',
    rootRunId: 'goal-root',
    mode: 'goal',
    processId: 'packaging_studio',
    query: 'Build the deck',
    status: 'running',
    artifactId: 'goal-root',
  });
  const stage = message('stage-delivery', {
    id: 'stage-run',
    rootRunId: 'goal-root',
    parentRunId: 'goal-root',
    mode: 'artifacts',
    query: 'Compile the work',
    status: 'complete',
    artifactId: 'stage-artifact',
    agentName: 'Designer',
    delegatedBy: 'Scout',
  });
  assert.equal(latestScoutWorkMessage([root, stage])?.id, 'deck-root');

  const governedStage: ScoutMessage = {
    id: 'governed-stage-delivery',
    kind: 'work_result',
    role: 'scout',
    text: 'The delegated work is ready.',
    createdAt: '2026-08-22T12:04:00Z',
    work: {
      id: 'governed-stage', runId: 'stage-run', title: 'Work', status: 'complete', workerName: 'Designer',
      rootRunId: 'goal-root', parentRunId: 'goal-root',
      currentStage: 'Done', summary: 'The delegated composition pass is ready.', progressPercent: 100,
      artifactId: 'stage-artifact', artifactHref: '/api/stride/v1/work/runs/stage-run/artifact',
      evidenceHref: '', providerExecutionFenced: false,
    },
  };
  assert.equal(latestScoutWorkMessage([root, governedStage])?.id, 'deck-root');
  assert.equal(latestScoutWorkMessage([governedStage])?.id, governedStage.id);
});

test('Activity ownership follows only explicit root topology and newest direct work supersedes an older typed result', () => {
  const olderResult = message('older-result', {
    id: 'old-root', rootRunId: 'old-root', mode: 'goal', query: 'Old deck', status: 'complete',
    artifactId: 'old-goal', resultArtifactId: 'old-deck', resultArtifactType: 'html_deck',
  });
  const newerGoverned: ScoutMessage = {
    id: 'newer-governed', kind: 'work_record', role: 'scout', createdAt: '2026-08-22T12:09:00Z',
    work: {
      id: 'new-run', runId: 'new-run', rootRunId: 'new-run', title: 'New direct work', status: 'running', workerName: 'Scout',
      currentStage: 'Research', summary: 'Working', artifactId: 'new-work', artifactHref: '', evidenceHref: '', providerExecutionFenced: false,
    },
  };
  assert.equal(latestScoutWorkMessage([olderResult, newerGoverned])?.id, 'newer-governed');

  const nameOnlyChild = message('name-only-child', {
    id: 'name-child', mode: 'work', query: 'Name-only delegation', status: 'running', artifactId: 'name-child-artifact',
    agentName: 'Designer', delegatedBy: 'Scout',
  });
  assert.equal(latestScoutWorkMessage([olderResult, nameOnlyChild])?.id, 'name-only-child', 'display copy cannot invent root ownership');
});

test('structured result kinds fail closed without their premium preview envelopes', () => {
  const digest = 'a'.repeat(64);
  const image = {
    id: 'image-run', mode: 'work', query: 'Create image', status: 'complete', artifactId: 'image-root',
    resultArtifactId: 'image-result', resultArtifactType: 'image', resultArtifactVersion: 2, resultArtifactDigest: digest,
  } satisfies ScoutWorkThreadRef;
  assert.equal(workRefHasClosedResultEnvelope(image), false);
  assert.equal(workRefHasClosedResultEnvelope({
    ...image,
    resultAssets: [{ ref: 'b'.repeat(64), kind: 'image', mime: 'image/png', name: 'hero.png' }],
  }), true);
  assert.equal(workRefHasClosedResultEnvelope({
    ...image,
    resultArtifactVersion: undefined,
    resultAssets: [{ ref: 'b'.repeat(64), kind: 'image', mime: 'image/png', name: 'hero.png' }],
  }), false, 'a preview without a positive result version must fail closed');
  assert.equal(workRefHasClosedResultEnvelope({
    ...image,
    resultArtifactDigest: '{"raw":"json"}',
    resultAssets: [{ ref: 'b'.repeat(64), kind: 'image', mime: 'image/png', name: 'hero.png' }],
  }), false, 'a preview without a 64-hex result digest must fail closed');

  const workbook = { ...image, id: 'book-run', resultArtifactType: 'workbook', resultArtifactId: 'book-result' };
  assert.equal(workRefHasClosedResultEnvelope(workbook), false);
  assert.equal(workRefHasClosedResultEnvelope({
    ...workbook,
    resultAssets: [{ ref: 'c'.repeat(64), kind: 'export', mime: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', name: 'model.xlsx' }],
    resultWorkbook: { fileName: 'model.xlsx', mime: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', sheetCount: 3, formulaCount: 42 },
  }), true);
  assert.equal(workRefHasClosedResultEnvelope({
    ...workbook,
    resultAssets: [{ ref: 'd'.repeat(64), kind: 'export', mime: 'application/pdf', name: 'model.pdf' }],
    resultWorkbook: { fileName: 'model.xlsx', mime: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', sheetCount: 3, formulaCount: 42 },
  }), false, 'a generic export must never become a Download XLSX action');
});

test('historical process results survive missing type only through closed process contracts', () => {
  const legacyDeck = message('legacy-process-deck', {
    id: 'goal-deck',
    mode: 'goal',
    processId: 'packaging_studio',
    query: 'Build the deck',
    status: 'complete',
    artifactId: 'goal-deck-root',
    resultArtifactId: 'deck-result',
    resultArtifactVersion: 1,
    resultArtifactDigest: exactDigest,
  });
  const legacyDocument = message('legacy-process-document', {
    id: 'goal-document',
    mode: 'goal',
    processId: 'document_report',
    query: 'Write the report',
    status: 'complete',
    artifactId: 'goal-document-root',
    resultArtifactId: 'document-result',
    resultArtifactVersion: 1,
    resultArtifactDigest: exactDigest,
  });
  const unknownProcess = message('unknown-process-result', {
    id: 'goal-unknown',
    mode: 'goal',
    processId: 'future_process',
    query: 'Make something',
    status: 'complete',
    artifactId: 'goal-unknown-root',
    resultArtifactId: 'unknown-result',
  });

  assert.equal(workResultArtifactKind(legacyDeck.thread), 'html_deck');
  assert.equal(workResultArtifactKind(legacyDocument.thread), 'markdown');
  assert.equal(workResultArtifactKind(unknownProcess.thread), '');
  assert.equal(workMessageHasPrimaryResult(legacyDeck), true);
  assert.equal(workMessageHasPrimaryResult(legacyDocument), true);
  assert.equal(workMessageHasPrimaryResult(unknownProcess), false);
  assert.equal(workResultArtifactKind({ ...legacyDeck.thread!, processId: 'packaging_studio_v8' }), '');
  assert.equal(workResultArtifactKind({ ...legacyDocument.thread!, processId: 'document_report_v2' }), '');
  assert.deepEqual(
    compactThreadWorkMessages([legacyDeck, legacyDocument, unknownProcess]).map(({ id }) => id),
    ['legacy-process-deck', 'legacy-process-document'],
  );
});

test('reply compaction preserves the human decision and final result while removing stage chatter', () => {
  const stage = (id: string, status: string): ScoutWorkThreadRef => ({
    id,
    mode: 'goal',
    processId: 'packaging_studio',
    query: 'Build the reply deck',
    status,
    artifactId: `root-${id}`,
  });
  const replyMessages = [
    message('reply-request'),
    message('reply-stage', stage('stage', 'running')),
    message('reply-decision', {
      ...stage('decision', 'needs_input'),
      checkpoint: {
        id: 'audience',
        stageId: 'story',
        question: 'Who should this lead with?',
        options: [{ id: 'operators', label: 'Operators', action: 'proceed' }],
      },
    }),
    message('reply-result', {
      ...stage('result', 'complete'),
      resultArtifactId: 'reply-deck',
      resultArtifactType: 'html_deck',
      resultArtifactVersion: 1,
      resultArtifactDigest: exactDigest,
      resultQualityState: 'admitted',
      resultCanPresent: true,
    }),
  ];
  assert.deepEqual(
    compactThreadWorkMessages(replyMessages).map(({ id }) => id),
    ['reply-request', 'reply-decision', 'reply-result'],
  );
});
