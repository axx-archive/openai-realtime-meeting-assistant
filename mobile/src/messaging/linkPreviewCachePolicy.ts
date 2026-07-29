import type { LinkPreview } from '../api/types';

export const LINK_PREVIEW_CACHE_VERSION = 1;
export const LINK_PREVIEW_CACHE_MAX_ENTRIES = 80;
export const LINK_PREVIEW_CACHE_FRESH_MS = 7 * 24 * 60 * 60 * 1000;
export const LINK_PREVIEW_CACHE_STALE_MS = 30 * 24 * 60 * 60 * 1000;

export type LinkPreviewCacheEntry = {
  preview: LinkPreview;
  cachedAt: number;
};

export type LinkPreviewCacheSnapshot = {
  version: typeof LINK_PREVIEW_CACHE_VERSION;
  entries: Record<string, LinkPreviewCacheEntry>;
};

const previewStringKeys = [
  'url',
  'kind',
  'title',
  'description',
  'siteName',
  'imageUrl',
  'mediaType',
  'authorName',
  'authorHandle',
  'publishedAt',
] as const;

function sanitizedPreview(value: unknown): LinkPreview | null {
  if (!value || typeof value !== 'object') return null;
  const source = value as Record<string, unknown>;
  if (typeof source.url !== 'string' || !/^https?:\/\//i.test(source.url)) return null;
  const preview: LinkPreview = { url: source.url };
  for (const key of previewStringKeys) {
    if (key === 'url') continue;
    const candidate = source[key];
    if (typeof candidate === 'string' && candidate.length <= 12_000) {
      preview[key] = candidate;
    }
  }
  return preview;
}

export function normalizeLinkPreviewCacheSnapshot(
  value: unknown,
  now = Date.now(),
  maxEntries = LINK_PREVIEW_CACHE_MAX_ENTRIES,
): LinkPreviewCacheSnapshot {
  const source = value && typeof value === 'object' ? value as Record<string, unknown> : {};
  const rawEntries = source.version === LINK_PREVIEW_CACHE_VERSION && source.entries && typeof source.entries === 'object'
    ? source.entries as Record<string, unknown>
    : {};

  const entries = Object.entries(rawEntries)
    .flatMap(([url, raw]) => {
      if (!raw || typeof raw !== 'object') return [];
      const record = raw as Record<string, unknown>;
      const preview = sanitizedPreview(record.preview);
      const cachedAt = typeof record.cachedAt === 'number' ? record.cachedAt : Number.NaN;
      if (!preview || preview.url !== url || !Number.isFinite(cachedAt)) return [];
      if (cachedAt > now + 60_000 || now - cachedAt > LINK_PREVIEW_CACHE_STALE_MS) return [];
      return [[url, { preview, cachedAt }] as const];
    })
    .sort((left, right) => right[1].cachedAt - left[1].cachedAt)
    .slice(0, Math.max(0, maxEntries));

  return {
    version: LINK_PREVIEW_CACHE_VERSION,
    entries: Object.fromEntries(entries),
  };
}
