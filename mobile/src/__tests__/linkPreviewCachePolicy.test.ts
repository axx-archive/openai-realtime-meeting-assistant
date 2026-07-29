import assert from 'node:assert/strict';
import test from 'node:test';

import {
  LINK_PREVIEW_CACHE_STALE_MS,
  LINK_PREVIEW_CACHE_VERSION,
  normalizeLinkPreviewCacheSnapshot,
} from '../messaging/linkPreviewCachePolicy';

test('link preview cache keeps recent sanitized entries and removes expired data', () => {
  const now = 2_000_000_000_000;
  const result = normalizeLinkPreviewCacheSnapshot({
    version: LINK_PREVIEW_CACHE_VERSION,
    entries: {
      'https://example.com/recent': {
        cachedAt: now - 1_000,
        preview: { url: 'https://example.com/recent', title: 'Recent', unknown: 'discard me' },
      },
      'https://example.com/expired': {
        cachedAt: now - LINK_PREVIEW_CACHE_STALE_MS - 1,
        preview: { url: 'https://example.com/expired', title: 'Old' },
      },
    },
  }, now);

  assert.deepEqual(result.entries, {
    'https://example.com/recent': {
      cachedAt: now - 1_000,
      preview: { url: 'https://example.com/recent', title: 'Recent' },
    },
  });
});

test('link preview cache remains bounded to the newest entries', () => {
  const now = 2_000_000_000_000;
  const result = normalizeLinkPreviewCacheSnapshot({
    version: LINK_PREVIEW_CACHE_VERSION,
    entries: Object.fromEntries(Array.from({ length: 5 }, (_, index) => [
      `https://example.com/${index}`,
      { cachedAt: now - index, preview: { url: `https://example.com/${index}` } },
    ])),
  }, now, 2);

  assert.deepEqual(Object.keys(result.entries), ['https://example.com/0', 'https://example.com/1']);
});

test('link preview cache rejects mismatched and non-http URL records', () => {
  const now = 2_000_000_000_000;
  const result = normalizeLinkPreviewCacheSnapshot({
    version: LINK_PREVIEW_CACHE_VERSION,
    entries: {
      'https://example.com': { cachedAt: now, preview: { url: 'https://different.example' } },
      'file:///secret': { cachedAt: now, preview: { url: 'file:///secret' } },
    },
  }, now);

  assert.deepEqual(result.entries, {});
});
