import { File, Paths } from 'expo-file-system';
import * as Sharing from 'expo-sharing';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';
import { buildApiUrl, buildAuthHeaders } from '../api/requestHelpers';

export type RemoteFile = {
  name: string;
  mime?: string;
  downloadUrl?: string;
  ref?: string;
};

function safeLocalName(name: string): string {
  const trimmed = name.trim() || 'Bonfire file';
  return trimmed
    .replace(/[\x00-\x1f\x7f/\\:?*"<>|]/g, '-')
    .replace(/^\.+/, '')
    .slice(0, 140) || 'Bonfire file';
}

function stableCacheKey(value: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(36);
}

export function remoteFilePath(file: RemoteFile): string {
  if (file.downloadUrl?.trim()) return file.downloadUrl.trim();
  if (!file.ref?.trim()) return '';
  return `/artifacts/blob?ref=${encodeURIComponent(file.ref.trim())}&name=${encodeURIComponent(file.name || 'file')}`;
}

export function authenticatedFileUrl(file: RemoteFile): string {
  const path = remoteFilePath(file);
  if (!path) return '';
  const url = /^https?:\/\//i.test(path) ? path : buildApiUrl(API_BASE_URL, path);
  try {
    const resolved = new URL(url);
    if (resolved.origin !== new URL(API_BASE_URL).origin) return '';
    return resolved.toString();
  } catch {
    return '';
  }
}

export function authenticatedFileHeaders(sessionToken: string, mime = ''): Record<string, string> {
  return buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken, {
    Accept: mime.trim() || '*/*',
  });
}

export function isInlinePreviewable(file: RemoteFile): boolean {
  return ['application/pdf', 'image/png', 'image/jpeg', 'image/gif', 'image/webp']
    .includes((file.mime ?? '').trim().toLowerCase());
}

export async function downloadRemoteFile(
  sessionToken: string,
  file: RemoteFile,
): Promise<File> {
  const url = authenticatedFileUrl(file);
  if (!url) throw new Error('This item does not include downloadable file data.');

  const destination = new File(
    Paths.cache,
    `bonfire-preview-${stableCacheKey(remoteFilePath(file))}-${safeLocalName(file.name)}`,
  );
  if (destination.exists && destination.size > 0) return destination;
  return File.downloadFileAsync(url, destination, {
    headers: authenticatedFileHeaders(sessionToken, file.mime),
    idempotent: true,
  });
}

export async function shareOrSaveRemoteFile(
  sessionToken: string,
  file: RemoteFile,
): Promise<void> {
  if (!(await Sharing.isAvailableAsync())) {
    throw new Error('Sharing is not available on this device.');
  }
  const localFile = await downloadRemoteFile(sessionToken, file);
  await Sharing.shareAsync(localFile.uri, {
    mimeType: file.mime || undefined,
    dialogTitle: `Share or save ${file.name}`,
  });
}
