export function nativeDeckFrame(width: number, height: number) {
  const compact = width < 700;
  const horizontalPadding = compact ? 16 : 32;
  const verticalChrome = compact ? 160 : 170;
  const availableWidth = Math.max(240, width - horizontalPadding * 2);
  const availableHeight = Math.max(160, height - verticalChrome);
  const frameWidth = Math.min(availableWidth, availableHeight * (16 / 9));
  return {
    width: Math.round(frameWidth),
    height: Math.round(frameWidth * (9 / 16)),
    compact,
  };
}

export function nativeDeckRenderPath(value: unknown): string | null {
  const path = String(value ?? '').trim();
  return path.startsWith('/artifacts/render?') && !path.startsWith('//') ? path : null;
}

export function nativeDeckPreviewPath(
  artifactId: unknown,
  version: unknown,
  digest: unknown,
): string | null {
  const id = String(artifactId ?? '').trim();
  const exactVersion = Number(version);
  const exactDigest = String(digest ?? '').trim().toLowerCase();
  if (!id || !Number.isSafeInteger(exactVersion) || exactVersion < 1 || !/^[0-9a-f]{64}$/u.test(exactDigest)) {
    return null;
  }
  return `/artifacts/preview?id=${encodeURIComponent(id)}&version=${encodeURIComponent(String(exactVersion))}&digest=${encodeURIComponent(exactDigest)}`;
}

/**
 * Native text surfaces render prose, never serialized Studio state or markup.
 * An untyped payload fails closed until authoritative artifact metadata can
 * route it to the deck or document viewer.
 */
export function nativeTextArtifactIsRenderable(value: unknown): boolean {
  const text = String(value ?? '').trim();
  if (!text || text.startsWith('<')) return false;
  try {
    JSON.parse(text);
    // A valid JSON primitive is still serialized application state, not a
    // prose deliverable. Fail closed for every JSON shape rather than only
    // objects and arrays so unknown legacy artifacts cannot leak as code.
    return false;
  } catch {
    return true;
  }
}
