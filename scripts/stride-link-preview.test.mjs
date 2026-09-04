import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
const start = html.indexOf('function requestDesktopChatLinkPreview(url)');
const end = html.indexOf('function mountDesktopChatLinkPreview(stack, text)', start);
assert.ok(start > 0 && end > start);
function harness(fetch) {
  const context = vm.createContext({ fetch, Map, Date, Error, encodeURIComponent, AbortSignal, desktopChatPreviewCache: null });
  vm.runInContext(html.slice(start, end), context);
  return context;
}
const response = title => ({ ok: true, json: async () => ({ preview: { title } }) });

test('concurrent preview readers share a request, and a transient failure can recover', async () => {
  let calls = 0;
  const c = harness(async () => {
    if (++calls === 1) throw new Error('offline');
    return response('Recovered article');
  });
  const first = c.requestDesktopChatLinkPreview('https://example.com/story');
  assert.equal(c.requestDesktopChatLinkPreview('https://example.com/story'), first);
  await assert.rejects(first, /offline/);
  assert.equal(c.desktopChatPreviewCache.size, 0);
  assert.equal((await c.requestDesktopChatLinkPreview('https://example.com/story')).title, 'Recovered article');
  assert.equal(calls, 2);
});

test('successful metadata expires and the cache stays bounded', async () => {
  let calls = 0;
  const c = harness(async () => response(`Article ${++calls}`));
  await c.requestDesktopChatLinkPreview('https://example.com/story');
  c.desktopChatPreviewCache.get('https://example.com/story').expiresAt = 0;
  assert.equal((await c.requestDesktopChatLinkPreview('https://example.com/story')).title, 'Article 2');
  for (let i = 0; i < 140; i++) await c.requestDesktopChatLinkPreview(`https://example.com/${i}`);
  assert.equal(c.desktopChatPreviewCache.size, 128);
});

test('a late response from a replaced session cannot return its metadata', async () => {
  let resolve;
  const c = harness(() => new Promise(r => { resolve = r; }));
  const old = c.requestDesktopChatLinkPreview('https://example.com/story');
  c.desktopChatPreviewCache = new Map();
  resolve(response('Previous session'));
  await assert.rejects(old, /preview unavailable/);
  assert.equal(c.desktopChatPreviewCache.size, 0);
});
