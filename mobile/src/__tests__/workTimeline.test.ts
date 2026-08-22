import assert from 'node:assert/strict';
import test from 'node:test';

import type { ScoutMessage, ScoutWorkThreadRef } from '../api/types';
import {
  compactThreadWorkMessages,
  latestScoutWorkMessage,
  workMessageHasActionableDecision,
  workMessageHasPrimaryResult,
} from '../messaging/workTimeline';

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
      resultTitle: 'Field Notes',
    }),
    message('deck-v2', {
      ...process('root'),
      resultArtifactId: 'deck-1',
      resultArtifactType: 'html_deck',
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

test('non-actionable needs-input cards and internal stage deliverables stay in Activity', () => {
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
  });
  assert.equal(workMessageHasActionableDecision(hidden), false);
  assert.deepEqual(
    compactThreadWorkMessages([hidden, legacyDeck, directResearch, reportResult]).map(({ id }) => id),
    ['legacy-deck', 'direct-research', 'report-result'],
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
      resultQualityState: 'admitted',
      resultCanPresent: true,
    }),
  ];
  assert.deepEqual(
    compactThreadWorkMessages(replyMessages).map(({ id }) => id),
    ['reply-request', 'reply-decision', 'reply-result'],
  );
});
