import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

import type { ScoutThread, ScoutWorkThreadRef } from '../api/types';

type Preview = (thread: ScoutThread) => string;

function loadPreviewContract(): Preview {
  const source = fs.readFileSync(path.resolve(import.meta.dirname, '..', 'messaging', 'ChannelList.tsx'), 'utf8');
  const start = '/* channel-terminal-preview-contract:start */';
  const end = '/* channel-terminal-preview-contract:end */';
  const first = source.indexOf(start);
  const last = source.indexOf(end);
  assert.ok(first >= 0 && last > first, 'terminal preview contract region must remain closed');
  const region = source.slice(first + start.length, last).replace('export function channelTerminalPreview', 'function channelTerminalPreview');
  const compiled = ts.transpileModule(region, {
    compilerOptions: { module: ts.ModuleKind.None, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const context: { preview?: Preview } = {};
  vm.runInNewContext(`${compiled}\nthis.preview = channelTerminalPreview;`, context);
  assert.equal(typeof context.preview, 'function');
  return context.preview as Preview;
}

const project = loadPreviewContract();

function thread(overrides: Partial<ScoutThread> = {}): ScoutThread {
  return {
    id: 'channel-1',
    preview: 'research workstream confirmed — running now',
    lastMessage: { text: 'research workstream confirmed — running now' },
    messages: [],
    ...overrides,
  };
}

function work(status: string, overrides: Partial<ScoutWorkThreadRef> = {}): ScoutWorkThreadRef {
  return {
    id: 'run-current',
    artifactId: 'artifact-current',
    mode: 'research',
    query: 'current market evidence',
    status,
    ...overrides,
  };
}

test('terminal research completion overrides stale launch copy with exact current source summary', () => {
  assert.equal(project(thread({
    preview: 'Research delivered · 12 cited source links · 10 domains',
    lastMessage: { text: 'research workstream confirmed — running now' },
    messages: [{ id: 'work', kind: 'thread', role: 'scout', createdAt: '2026-08-09T20:00:00Z', text: 'Research delivered · 12 cited source links · 10 domains', thread: work('complete') }],
  })), 'Research delivered · 12 cited source links · 10 domains');
});

test('completion never derives current provenance from body, history, or malformed preview copy', () => {
  for (const preview of [
    'Research delivered · 01 cited source links · 10 domains',
    'Research delivered · 1 cited source links · 1 domains',
    'Research delivered · 10001 cited source links · 10 domains',
    'Research delivered · 12 cited source links · 10001 domains',
    'Research delivered · 35 sources',
    'research workstream confirmed — running now',
  ]) {
    assert.equal(project(thread({
      preview,
      lastMessage: { text: '35 sources from current v2 plus previous v1 history' },
      messages: [{ id: 'work', kind: 'thread', role: 'scout', createdAt: '2026-08-09T20:00:00Z', text: 'https://private.example/body', thread: work('complete') }],
    })), 'Research delivered');
  }
  assert.equal(project(thread({
    preview: 'Research delivered · 1 cited source link · 1 domain',
    messages: [{ id: 'work', kind: 'thread', role: 'scout', createdAt: '2026-08-09T20:00:00Z', text: 'Research delivered · 1 cited source link · 1 domain', thread: work('complete') }],
  })), 'Research delivered · 1 cited source link · 1 domain');
});

test('terminal failure is body-free and stale running copy cannot win', () => {
  const rendered = project(thread({
    preview: 'provider secret raw failure: sk-do-not-project',
    lastMessage: { text: 'research workstream confirmed — running now' },
    messages: [{ id: 'work', kind: 'thread', role: 'scout', createdAt: '2026-08-09T20:00:00Z', text: 'private error body', thread: work('error') }],
  }));
  assert.equal(rendered, 'Needs attention');
  assert.doesNotMatch(rendered, /provider|secret|private|sk-/iu);
});

test('active work owns bounded copy and incomplete or unbound authority cannot mint terminal copy', () => {
  const active = thread({
    preview: 'Research delivered · 12 cited source links · 10 domains',
    lastMessage: { text: 'Research delivered · 12 cited source links · 10 domains' },
    messages: [
      { id: 'complete', kind: 'thread', role: 'scout', createdAt: '2026-08-09T19:00:00Z', thread: work('complete', { id: 'older-run' }) },
      { id: 'active', kind: 'thread', role: 'scout', createdAt: '2026-08-09T20:00:00Z', thread: work('running') },
    ],
  });
  assert.equal(project(active), 'Scout is working');

  for (const authority of [
    work('complete', { artifactId: '' }),
    work('complete', { id: '' }),
    work('complete', { mode: 'design' }),
    work('completed'),
  ]) {
    assert.equal(project(thread({ messages: [{ id: 'work', kind: 'thread', role: 'scout', createdAt: '2026-08-09T20:00:00Z', thread: authority }] })), 'research workstream confirmed — running now');
  }
});

test('a valid-looking source preview without the matching terminal card degrades to generic delivery', () => {
  assert.equal(project(thread({
    preview: 'Research delivered · 12 cited source links · 10 domains',
    messages: [{ id: 'work', kind: 'thread', role: 'scout', createdAt: '2026-08-09T20:00:00Z', text: 'research workstream confirmed — running now', thread: work('complete') }],
  })), 'Research delivered');
});

test('the component renders only the closed projection and does not fetch artifacts for list copy', () => {
  const source = fs.readFileSync(path.resolve(import.meta.dirname, '..', 'messaging', 'ChannelList.tsx'), 'utf8');
  assert.match(source, /const body = channelTerminalPreview\(thread\)/u);
  assert.doesNotMatch(source, /api\.artifact\(/u);
  assert.doesNotMatch(source, /researchCitationCount|researchSourceDomainCount|threadRuns/u);
});
