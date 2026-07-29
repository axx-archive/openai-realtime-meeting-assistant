import { File, Paths } from 'expo-file-system';

import type { LinkPreview } from '../api/types';
import {
  LINK_PREVIEW_CACHE_FRESH_MS,
  LINK_PREVIEW_CACHE_MAX_ENTRIES,
  LINK_PREVIEW_CACHE_VERSION,
  normalizeLinkPreviewCacheSnapshot,
  type LinkPreviewCacheEntry,
  type LinkPreviewCacheSnapshot,
} from './linkPreviewCachePolicy';

type PreviewLoader = () => Promise<LinkPreview | null>;

const cacheFile = new File(Paths.cache, 'bonfire-link-previews-v1.json');
const entries = new Map<string, LinkPreviewCacheEntry>();
const pending = new Map<string, Promise<LinkPreview | null>>();
let writeChain = Promise.resolve();

const hydration = (async () => {
  try {
    if (!cacheFile.exists) return;
    const parsed = JSON.parse(await cacheFile.text()) as unknown;
    const snapshot = normalizeLinkPreviewCacheSnapshot(parsed);
    for (const [url, entry] of Object.entries(snapshot.entries)) entries.set(url, entry);
  } catch {
    // A corrupt or evicted cache is just a cache miss.
  }
})();

function snapshot(): LinkPreviewCacheSnapshot {
  const normalized = normalizeLinkPreviewCacheSnapshot({
    version: LINK_PREVIEW_CACHE_VERSION,
    entries: Object.fromEntries(entries),
  });
  entries.clear();
  for (const [url, entry] of Object.entries(normalized.entries)) entries.set(url, entry);
  return normalized;
}

function persist(): void {
  writeChain = writeChain.then(async () => {
    try {
      if (!cacheFile.exists) cacheFile.create({ intermediates: true });
      cacheFile.write(JSON.stringify(snapshot()));
    } catch {
      // Network previews still work when the OS cache directory is unavailable.
    }
  });
}

async function refresh(url: string, loader: PreviewLoader): Promise<LinkPreview | null> {
  const existing = pending.get(url);
  if (existing) return existing;
  const request = loader()
    .then((preview) => {
      if (preview) {
        entries.set(url, { preview: { ...preview, url }, cachedAt: Date.now() });
        if (entries.size > LINK_PREVIEW_CACHE_MAX_ENTRIES) snapshot();
        persist();
      }
      return preview;
    })
    .catch(() => null)
    .finally(() => pending.delete(url));
  pending.set(url, request);
  return request;
}

export async function cachedLinkPreview(url: string, loader: PreviewLoader): Promise<LinkPreview | null> {
  await hydration;
  const cached = entries.get(url);
  if (cached) {
    if (Date.now() - cached.cachedAt > LINK_PREVIEW_CACHE_FRESH_MS) void refresh(url, loader);
    return cached.preview;
  }
  return refresh(url, loader);
}
