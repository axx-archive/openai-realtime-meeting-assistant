import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const source = (...parts: string[]) => readFileSync(path.resolve(import.meta.dirname, '..', ...parts), 'utf8');

test('native Chat loads a body-free index and reserves stable rows immediately', () => {
  const client = source('api', 'client.ts');
  const list = source('messaging', 'ChannelList.tsx');
  assert.match(client, /scoutThreadIndex[\s\S]*?\/assistant\/chat-threads\?view=index/);
  assert.match(list, /api\.scoutThreadIndex\(token\)/);
  assert.match(list, /accessibilityLabel="Loading channels and private chats"/);
  assert.match(list, /Array\.from\(\{ length: 6 \}/);
});

test('native Chat coalesces loads and fences identity changes', () => {
  const list = source('messaging', 'ChannelList.tsx');
  assert.match(list, /if \(loadRequestRef\.current\)[\s\S]*?loadQueuedRef\.current = true/);
  assert.match(list, /generation !== loadGenerationRef\.current/);
  assert.match(list, /if \(loadRequestRef\.current === request\) loadRequestRef\.current = null/);
  assert.match(list, /loadGenerationRef\.current \+= 1;[\s\S]*?setThreads\(\[\]\)/);
});
