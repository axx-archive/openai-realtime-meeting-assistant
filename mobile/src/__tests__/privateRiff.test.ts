import assert from 'node:assert/strict';
import test from 'node:test';

import type { PrivateRiffBinding, ScoutMessage } from '../api/types';
import {
  initialPrivateRiffParagraphTokens,
  privateRiffAnswerShareable,
  privateRiffCheckpointSummary,
  privateRiffHasUpdates,
  selectedPrivateRiffParagraphTokens,
} from '../messaging/privateRiff';

const riff: PrivateRiffBinding = {
  sourceThreadId: 'channel-1',
  sourceTitle: 'design-room',
  throughMessageId: 'message-2',
  throughAuthorName: 'Alex',
  throughCreatedAt: '2026-08-15T16:30:00.000Z',
  messageCount: 2,
  contextRevision: 1,
  capturedAt: '2026-08-15T16:31:00.000Z',
  agentName: 'Scout',
  sourceAvailable: true,
  newMessageCount: 3,
};

test('the checkpoint names its source and only reports authorized updates', () => {
  assert.match(privateRiffCheckpointSummary(riff), /^Private · grounded in #design-room through /);
  assert.equal(privateRiffHasUpdates(riff), true);
  assert.equal(privateRiffHasUpdates({ ...riff, sourceAvailable: false }), false);
  assert.equal(privateRiffHasUpdates({ ...riff, newMessageCount: 0 }), false);
});

test('only server-bound complete Scout answers in an available riff can cross the boundary', () => {
  const answer = {
    id: 'answer-1', role: 'assistant', text: 'A considered answer.',
    activity: {
      version: 'stride-private-riff/v1', status: 'completed', stage: 'answered_from_checkpoint',
      startedAt: '2026-08-15T16:31:00Z', completedAt: '2026-08-15T16:31:02Z', elapsedMs: 2000,
      sourceCount: 2, evidenceKind: 'channel_checkpoint', rationale: 'Safe rationale.', contextRevision: 1,
      sourceThreadId: 'channel-1', throughMessageId: 'message-2',
    },
  } as ScoutMessage;
  assert.equal(privateRiffAnswerShareable(riff, answer), true);
  assert.equal(privateRiffAnswerShareable(riff, { ...answer, activity: undefined }), false);
  assert.equal(privateRiffAnswerShareable(riff, { ...answer, role: 'user' }), false);
  assert.equal(privateRiffAnswerShareable(riff, { ...answer, text: '' }), false);
  assert.equal(privateRiffAnswerShareable(riff, {
    ...answer,
    reply: { state: 'running', operationId: 'op-1', inReplyTo: 'prompt-1', attempt: 1 },
  }), false);
  assert.equal(privateRiffAnswerShareable({ ...riff, sourceAvailable: false }, answer), false);
});

test('paragraph selection rejects empty candidates and preserves server order', () => {
  const paragraphs = [
    { token: 'p-1', text: 'First' },
    { token: '', text: 'No token' },
    { token: 'p-2', text: 'Second' },
    { token: 'p-3', text: '   ' },
  ];
  assert.deepEqual([...initialPrivateRiffParagraphTokens(paragraphs)], ['p-1', 'p-2']);
  assert.deepEqual(selectedPrivateRiffParagraphTokens(paragraphs, new Set(['p-2', 'p-1'])), ['p-1', 'p-2']);
});
