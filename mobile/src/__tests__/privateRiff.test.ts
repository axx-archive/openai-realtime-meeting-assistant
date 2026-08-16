import assert from 'node:assert/strict';
import test from 'node:test';

import type { PrivateRiffBinding, ScoutMessage } from '../api/types';
import {
  privateRiffCheckpointSummary,
  privateRiffCurrentEpisodeMessages,
  privateRiffFreshnessSummary,
  privateRiffHasUpdates,
  privateRiffPacificDateTime,
  privateRiffReplyAuthor,
  privateRiffReplyShareable,
  privateRiffShareAllCount,
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
  autoFresh: true,
  activeEpisodeId: 'episode-current',
  episodeCount: 2,
};

const turns = [
  { id: 'root', role: 'user', authorName: 'AJ', text: 'Help me think this through.' },
  {
    id: 'scout-reply', role: 'scout', authorName: 'Scout', text: 'Here is a direction.',
    activity: { version: 'stride-private-riff/v1', status: 'completed', contextRevision: 1, throughMessageId: 'message-2' },
  },
  { id: 'user-reply', role: 'user', authorName: 'AJ', text: 'That framing works.' },
] as ScoutMessage[];

test('the checkpoint names its source and reports automatic freshness without a refresh burden', () => {
  assert.equal(privateRiffCheckpointSummary(riff), 'Private Riff · #design-room through Aug 15, 2026, 9:30 AM PDT');
  assert.equal(privateRiffFreshnessSummary(riff), 'Channel context stays current automatically');
  assert.equal(privateRiffFreshnessSummary({ ...riff, sourceAvailable: false, unavailableReason: 'Access changed' }), 'Access changed');
  assert.equal(privateRiffPacificDateTime('2026-01-15T20:30:00.000Z'), 'Jan 15, 2026, 12:30 PM PST');
  assert.equal(privateRiffHasUpdates(riff), true);
  assert.equal(privateRiffHasUpdates({ ...riff, sourceAvailable: false }), false);
  assert.equal(privateRiffHasUpdates({ ...riff, newMessageCount: 0 }), false);
});

test('the visible transcript and share-all boundary stay in the active episode', () => {
  const prior = { ...turns[0], id: 'prior', riffEpisodeId: 'episode-prior' };
  const current = turns.map((message) => ({ ...message, riffEpisodeId: 'episode-current' }));
  assert.deepEqual(
    privateRiffCurrentEpisodeMessages(riff, [prior, ...current]).map((message) => message.id),
    ['root', 'scout-reply', 'user-reply'],
  );
  assert.equal(privateRiffShareAllCount([prior, ...current], riff), 3);
  assert.equal(privateRiffCurrentEpisodeMessages(riff, [prior]).length, 0);
  assert.deepEqual(
    privateRiffCurrentEpisodeMessages({ ...riff, viewedEpisodeId: 'episode-prior' }, [prior, ...current]).map((message) => message.id),
    ['prior'],
  );
  assert.equal(privateRiffCurrentEpisodeMessages({ ...riff, activeEpisodeId: undefined }, [prior]).length, 1);
});

test('any complete non-root human or Scout reply is shareable under server authorship', () => {
  assert.equal(privateRiffReplyShareable(riff, turns[0], turns), false);
  assert.equal(privateRiffReplyShareable(riff, turns[1], turns), true);
  assert.equal(privateRiffReplyShareable(riff, turns[2], turns), true);
  assert.equal(privateRiffReplyAuthor(turns[1]), 'Scout');
  assert.equal(privateRiffReplyAuthor(turns[2]), 'AJ');
  assert.equal(privateRiffReplyShareable({ ...riff, sourceAvailable: false }, turns[1], turns), false);
  assert.equal(privateRiffReplyShareable(riff, { ...turns[1], reply: { state: 'running', operationId: 'op', inReplyTo: 'root', attempt: 1 } }, turns), false);
});

test('share-all counts the authored conversation and excludes incomplete placeholders', () => {
  const queued = { id: 'queued', role: 'scout', text: 'Working', reply: { state: 'queued', operationId: 'op', inReplyTo: 'root', attempt: 1 } } as ScoutMessage;
  assert.equal(privateRiffShareAllCount([...turns, queued]), 3);
});
