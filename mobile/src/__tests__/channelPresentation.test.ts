import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import path from 'node:path';

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

test('the mobile thread list separates channels and private work with an icon-only pin', () => {
  const source = fs.readFileSync(path.resolve(import.meta.dirname, '..', 'messaging', 'ChannelList.tsx'), 'utf8');
  const rows = fs.readFileSync(path.resolve(import.meta.dirname, '..', 'messaging', 'channelListPerformance.ts'), 'utf8');
  assert.match(rows, /label: 'CHANNELS'/);
  assert.match(rows, /label: 'PRIVATE'/);
  assert.match(rows, /thread\.visibility === 'public'/);
  assert.match(source, /name="pin\.fill"/);
  assert.doesNotMatch(source, />STRIDE</);
});
