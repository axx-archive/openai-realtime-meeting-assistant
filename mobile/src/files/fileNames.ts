const maxLocalFileNameLength = 140;

const extensionsByMime: Record<string, readonly string[]> = {
  'application/pdf': ['.pdf'],
  'image/gif': ['.gif'],
  'image/jpeg': ['.jpg', '.jpeg'],
  'image/png': ['.png'],
  'image/webp': ['.webp'],
};

function normalizedMime(mime: string | undefined): string {
  return (mime ?? '').split(';', 1)[0].trim().toLowerCase();
}

/**
 * Gives downloaded files a type-bearing local name. WKWebView and iOS share
 * services classify file:// URLs by extension, while Drive display titles are
 * intentionally allowed to omit one (for example, generated PDF reports).
 */
export function localFileName(name: string, mime?: string): string {
  const fallback = 'Stride file';
  const sanitized = (name.trim() || fallback)
    .replace(/[\x00-\x1f\x7f/\\:?*"<>|]/g, '-')
    .replace(/^\.+/, '') || fallback;
  const extensions = extensionsByMime[normalizedMime(mime)] ?? [];
  const hasExpectedExtension = extensions.some((extension) => sanitized.toLowerCase().endsWith(extension));
  const extension = hasExpectedExtension ? '' : (extensions[0] ?? '');
  const baseLimit = Math.max(1, maxLocalFileNameLength - extension.length);
  return `${sanitized.slice(0, baseLimit)}${extension}`;
}
