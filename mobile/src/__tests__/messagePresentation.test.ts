import assert from 'node:assert/strict';
import test from 'node:test';

import {
  activeMentionQuery,
  completeMention,
  extractHttpUrls,
  groupMessageReactions,
  isOwnMessageForViewer,
  parseMessageTextSegments,
} from '../messaging/messagePresentation';

test('message segmentation preserves mentions and lifts safe URLs first', () => {
  assert.deepEqual(
    parseMessageTextSegments('hey @tyler see https://x.com/@alex/status/123?s=46, then ask @scout.'),
    [
      { kind: 'text', text: 'hey ' },
      { kind: 'mention', text: '@tyler', name: 'tyler', scout: false },
      { kind: 'text', text: ' see ' },
      {
        kind: 'link',
        text: 'https://x.com/@alex/status/123?s=46',
        url: 'https://x.com/@alex/status/123?s=46',
      },
      { kind: 'text', text: ', then ask ' },
      { kind: 'mention', text: '@scout', name: 'scout', scout: true },
      { kind: 'text', text: '.' },
    ],
  );
});

test('URL extraction leaves trailing prose punctuation outside the link', () => {
  const text = 'Read (https://example.com/report?q=one). Then https://example.com/two!';
  assert.deepEqual(extractHttpUrls(text), [
    {
      url: 'https://example.com/report?q=one',
      start: text.indexOf('https://example.com/report'),
      end: text.indexOf('https://example.com/report') + 'https://example.com/report?q=one'.length,
    },
    {
      url: 'https://example.com/two',
      start: text.indexOf('https://example.com/two'),
      end: text.indexOf('https://example.com/two') + 'https://example.com/two'.length,
    },
  ]);
  assert.equal(
    parseMessageTextSegments(text).map((segment) => segment.text).join(''),
    text,
  );
});

test('balanced closing brackets remain in a URL while unmatched ones do not', () => {
  assert.deepEqual(
    extractHttpUrls('https://en.wikipedia.org/wiki/Function_(mathematics) and https://example.com/a])'),
    [
      {
        url: 'https://en.wikipedia.org/wiki/Function_(mathematics)',
        start: 0,
        end: 'https://en.wikipedia.org/wiki/Function_(mathematics)'.length,
      },
      {
        url: 'https://example.com/a',
        start: 'https://en.wikipedia.org/wiki/Function_(mathematics) and '.length,
        end:
          'https://en.wikipedia.org/wiki/Function_(mathematics) and '.length +
          'https://example.com/a'.length,
      },
    ],
  );
});

test('non-http and malformed candidates remain plain text', () => {
  const text = 'javascript:alert(1) ftp://example.com HTTPS://';
  assert.deepEqual(extractHttpUrls(text), []);
  assert.deepEqual(parseMessageTextSegments(text), [{ kind: 'text', text }]);
});

test('reaction groups count unique normalized accounts and mark the viewer', () => {
  assert.deepEqual(
    groupMessageReactions(
      [
        { emoji: '👍', actorEmail: 'AJ@example.com' },
        { emoji: '👍', actorEmail: ' aj@example.com ' },
        { emoji: '👍', actorEmail: 'tim@example.com' },
        { emoji: ' ❤️ ', actorEmail: 'aj@example.com' },
        { emoji: '', actorEmail: 'nobody@example.com' },
        { emoji: '🔥', actorEmail: '' },
      ],
      'AJ@EXAMPLE.COM',
    ),
    [
      { emoji: '👍', count: 2, reactedByViewer: true },
      { emoji: '❤️', count: 1, reactedByViewer: true },
    ],
  );
});

test('reaction groups preserve first-seen emoji order and handle an anonymous viewer', () => {
  assert.deepEqual(
    groupMessageReactions(
      [
        { emoji: '🔥', actorEmail: 'tim@example.com' },
        { emoji: '👍', actorEmail: 'aj@example.com' },
        { emoji: '🔥', actorEmail: 'aj@example.com' },
      ],
      '',
    ),
    [
      { emoji: '🔥', count: 2, reactedByViewer: false },
      { emoji: '👍', count: 1, reactedByViewer: false },
    ],
  );
});

test('stamped messages belong only to their exact viewer account', () => {
  assert.equal(
    isOwnMessageForViewer(
      { role: 'user', authorEmail: 'AJ@example.com' },
      { viewerEmail: ' aj@example.com ', threadVisibility: 'public', threadOwnerEmail: 'other@example.com' },
    ),
    true,
  );
  assert.equal(
    isOwnMessageForViewer(
      { role: 'user', authorEmail: 'tim@example.com' },
      { viewerEmail: 'aj@example.com', threadVisibility: 'private', threadOwnerEmail: 'aj@example.com' },
    ),
    false,
  );
  assert.equal(
    isOwnMessageForViewer(
      { role: 'scout', authorEmail: 'aj@example.com' },
      { viewerEmail: 'aj@example.com', threadVisibility: 'private', threadOwnerEmail: 'aj@example.com' },
    ),
    false,
  );
});

test('unstamped public messages are never eligible, including for the thread owner', () => {
  assert.equal(
    isOwnMessageForViewer(
      { role: 'user' },
      { viewerEmail: 'aj@example.com', threadVisibility: ' PUBLIC ', threadOwnerEmail: 'aj@example.com' },
    ),
    false,
  );
});

test('legacy unstamped private messages are eligible only for the private owner', () => {
  const message = { role: 'user' };
  assert.equal(
    isOwnMessageForViewer(message, {
      viewerEmail: 'aj@example.com',
      threadVisibility: 'private',
      threadOwnerEmail: 'AJ@example.com',
    }),
    true,
  );
  assert.equal(
    isOwnMessageForViewer(message, {
      viewerEmail: 'tim@example.com',
      threadVisibility: 'private',
      threadOwnerEmail: 'aj@example.com',
    }),
    false,
  );
  assert.equal(
    isOwnMessageForViewer(message, { viewerEmail: 'aj@example.com' }),
    false,
  );
});

test('active mention completion replaces only the current trailing token', () => {
  assert.deepEqual(activeMentionQuery('Can you ask @ty'), { start: 12, query: 'ty' });
  assert.equal(completeMention('Can you ask @ty', 'Tyler'), 'Can you ask @Tyler ');
  assert.equal(completeMention('@sc', '@Scout'), '@Scout ');
  assert.deepEqual(activeMentionQuery('Ask @Insights-An'), { start: 4, query: 'Insights-An' });
  assert.equal(completeMention('Ask @Insights-An', 'Insights-Analyst'), 'Ask @Insights-Analyst ');
  assert.equal(activeMentionQuery('email aj@example.com'), null);
  assert.equal(completeMention('nothing active', 'Tyler'), 'nothing active');
});
