import assert from 'node:assert/strict';
import test from 'node:test';

import { channelDisplayName, isBonfireChat, pinBonfireChatFirst } from '../messaging/channelPresentation';
import type { ScoutThread } from '../api/types';

const channel = (id: string, title: string, table = false): ScoutThread => ({
  id,
  title,
  visibility: 'public',
  table,
  messages: [],
});

test('the shared Table is always presented as Bonfire Chat', () => {
  for (const thread of [channel('table', 'team', true), channel('legacy', 'general'), channel('named', 'Bonfire Chat')]) {
    assert.equal(isBonfireChat(thread), true);
    assert.equal(channelDisplayName(thread), 'Bonfire Chat');
  }
  assert.equal(channelDisplayName(channel('project', 'Ball Dogs')), '#Ball Dogs');
});

test('Bonfire Chat pins first without disturbing the remaining order', () => {
  const threads = [channel('a', 'Ball Dogs'), channel('table', 'team', true), channel('b', 'Country Golf')];
  assert.deepEqual(pinBonfireChatFirst(threads).map((thread) => thread.id), ['table', 'a', 'b']);
  assert.deepEqual(threads.map((thread) => thread.id), ['a', 'table', 'b']);
});
