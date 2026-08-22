export type StudioDownloadKind = 'deck' | 'document';
export type StudioDownloadFormat = 'pdf' | 'pptx';

export type StudioDownloadRequest = {
  kind: StudioDownloadKind;
  format: StudioDownloadFormat;
  artifactId: string;
  fileName: string;
  downloadUrl?: string;
  expectedVersion?: number;
  sceneRef?: string;
};

export type OSWebNavigationDecision =
  | { action: 'allow' }
  | { action: 'external'; url: string }
  | { action: 'block' };

type StudioRoute = {
  kind: StudioDownloadKind;
  artifactId: string;
  mode: 'edit' | 'present' | 'view';
};

const artifactIdPattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,159}$/;
const blobRefPattern = /^[0-9a-f]{64}$/;
const safeFileNamePattern = /^[^\x00-\x1f\x7f/\\:?*"<>|]{1,160}$/;

function parsedHTTPBase(baseUrl: string): URL | null {
  try {
    const base = new URL(baseUrl);
    if (base.protocol !== 'https:' && base.protocol !== 'http:') return null;
    return base;
  } catch {
    return null;
  }
}

function studioRoute(currentPath: string): StudioRoute | null {
  if (!currentPath.startsWith('/') || currentPath.startsWith('//')) return null;
  let destination: URL;
  try {
    destination = new URL(currentPath, 'https://stride.invalid');
  } catch {
    return null;
  }
  const match = destination.pathname.match(/^\/studio\/(deck|document)\/([^/]+)$/);
  if (!match) return null;
  let artifactId = '';
  try {
    artifactId = decodeURIComponent(match[2]);
  } catch {
    return null;
  }
  if (!artifactIdPattern.test(artifactId)) return null;
  const mode = destination.searchParams.get('mode');
  if (match[1] === 'deck' && mode !== 'edit' && mode !== 'present') return null;
  if (match[1] === 'document' && mode !== 'edit' && mode !== 'view') return null;
  return { kind: match[1] as StudioDownloadKind, artifactId, mode: mode as StudioRoute['mode'] };
}

function exactKeys(value: Record<string, unknown>, expected: readonly string[]): boolean {
  return Object.keys(value).sort().join(',') === [...expected].sort().join(',');
}

function validFileName(fileName: unknown, extension: '.pdf' | '.pptx'): fileName is string {
  return typeof fileName === 'string'
    && safeFileNamePattern.test(fileName)
    && fileName.toLowerCase().endsWith(extension);
}

function validatedBlobDownloadUrl(
  rawUrl: string,
  fileName: string,
  appBaseUrl: string,
): string | null {
  const base = parsedHTTPBase(appBaseUrl);
  if (!base) return null;
  let resolved: URL;
  try {
    resolved = new URL(rawUrl, base);
  } catch {
    return null;
  }
  if (resolved.origin !== base.origin || resolved.protocol !== base.protocol) return null;
  if (resolved.pathname !== '/artifacts/blob' || resolved.hash) return null;
  const keys = Array.from(resolved.searchParams.keys()).sort();
  if (keys.join(',') !== 'name,ref') return null;
  if (resolved.searchParams.getAll('name').length !== 1 || resolved.searchParams.getAll('ref').length !== 1) return null;
  if (!blobRefPattern.test(resolved.searchParams.get('ref') ?? '')) return null;
  if (resolved.searchParams.get('name') !== fileName) return null;
  return resolved.toString();
}

/**
 * Parses the tiny, versioned message contract emitted by Deck/Document Studio.
 * The current authenticated route is part of the authority: a document cannot
 * ask native iOS to export a deck, and one artifact cannot name another.
 */
export function parseStudioDownloadMessage(
  raw: string,
  currentPath: string,
  appBaseUrl: string,
): StudioDownloadRequest | null {
  if (!raw || raw.length > 2048) return null;
  const route = studioRoute(currentPath);
  if (!route) return null;
  let candidate: Record<string, unknown>;
  try {
    candidate = JSON.parse(raw) as Record<string, unknown>;
  } catch {
    return null;
  }
  if (!candidate || Array.isArray(candidate) || candidate.type !== 'stride.studio.download' || candidate.version !== 1) return null;
  if (candidate.kind !== route.kind || candidate.artifactId !== route.artifactId) return null;

  if (candidate.format === 'pdf') {
    if (!exactKeys(candidate, ['type', 'version', 'kind', 'format', 'artifactId', 'fileName', 'url'])) return null;
    if (!validFileName(candidate.fileName, '.pdf') || typeof candidate.url !== 'string') return null;
    const downloadUrl = validatedBlobDownloadUrl(candidate.url, candidate.fileName, appBaseUrl);
    if (!downloadUrl) return null;
    return {
      kind: route.kind,
      format: 'pdf',
      artifactId: route.artifactId,
      fileName: candidate.fileName,
      downloadUrl,
    };
  }

  if (candidate.format === 'pptx') {
    if (route.kind !== 'deck') return null;
    if (!exactKeys(candidate, ['type', 'version', 'kind', 'format', 'artifactId', 'fileName', 'expectedVersion', 'sceneRef'])) return null;
    if (!validFileName(candidate.fileName, '.pptx')) return null;
    if (!Number.isSafeInteger(candidate.expectedVersion) || Number(candidate.expectedVersion) < 1) return null;
    if (typeof candidate.sceneRef !== 'string' || !blobRefPattern.test(candidate.sceneRef)) return null;
    return {
      kind: 'deck',
      format: 'pptx',
      artifactId: route.artifactId,
      fileName: candidate.fileName,
      expectedVersion: Number(candidate.expectedVersion),
      sceneRef: candidate.sceneRef,
    };
  }

  return null;
}

export function isStudioDownloadBridgeMessage(raw: string): boolean {
  if (!raw || raw.length > 2048) return false;
  try {
    const candidate = JSON.parse(raw) as Record<string, unknown>;
    return Boolean(candidate && !Array.isArray(candidate) && candidate.type === 'stride.studio.download');
  } catch {
    return false;
  }
}

/**
 * Fallback for a direct <a download> response that WKWebView promotes through
 * onFileDownload. Blob/data URLs are deliberately unsupported; Studio's
 * message bridge carries PowerPoint's exact server export request instead.
 */
export function parseStudioFileDownloadUrl(
  rawUrl: string,
  currentPath: string,
  appBaseUrl: string,
): StudioDownloadRequest | null {
  const route = studioRoute(currentPath);
  const base = parsedHTTPBase(appBaseUrl);
  if (!route || !base) return null;
  let resolved: URL;
  try {
    resolved = new URL(rawUrl, base);
  } catch {
    return null;
  }
  const fileName = resolved.searchParams.get('name') ?? '';
  if (!validFileName(fileName, '.pdf')) return null;
  const downloadUrl = validatedBlobDownloadUrl(resolved.toString(), fileName, appBaseUrl);
  if (!downloadUrl) return null;
  return {
    kind: route.kind,
    format: 'pdf',
    artifactId: route.artifactId,
    fileName,
    downloadUrl,
  };
}

/** Exact-origin app navigation, narrowly safe external handoff, otherwise block. */
export function classifyOSWebNavigation(rawUrl: string, appBaseUrl: string): OSWebNavigationDecision {
  const base = parsedHTTPBase(appBaseUrl);
  if (!base) return { action: 'block' };
  let next: URL;
  try {
    next = new URL(rawUrl, base);
  } catch {
    return { action: 'block' };
  }
  if (next.origin === base.origin && next.protocol === base.protocol) return { action: 'allow' };
  // Never hand a same-host downgrade or alternate scheme to the operating system.
  if (next.hostname === base.hostname) return { action: 'block' };
  if (next.protocol === 'https:' || next.protocol === 'mailto:' || next.protocol === 'tel:') {
    return { action: 'external', url: next.toString() };
  }
  return { action: 'block' };
}
