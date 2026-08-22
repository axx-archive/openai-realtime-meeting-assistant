import { File, Paths } from 'expo-file-system';
import * as Sharing from 'expo-sharing';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';
import { buildAuthHeaders } from '../api/requestHelpers';
import { localFileName } from '../files/fileNames';
import type { StudioDownloadRequest } from './studioDownloadProtocol';

const pptxMime = 'application/vnd.openxmlformats-officedocument.presentationml.presentation';
const maxStudioDownloadBytes = 64 * 1024 * 1024;
const blobRefPattern = /^[0-9a-f]{64}$/;

function normalizedContentType(value: string | null): string {
  return (value ?? '').split(';', 1)[0].trim().toLowerCase();
}

function safeServerError(value: unknown, fallback: string): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return fallback;
  const error = (value as { error?: unknown }).error;
  if (typeof error !== 'string') return fallback;
  const normalized = error.replace(/[\x00-\x1f\x7f]/g, ' ').trim();
  return normalized && normalized.length <= 240 ? normalized : fallback;
}

function cacheDestination(request: StudioDownloadRequest, mime: string): File {
  return new File(Paths.cache, `stride-studio-${request.artifactId}-${localFileName(request.fileName, mime)}`);
}

function trustedApiBase(): URL {
  let base: URL;
  try {
    base = new URL(API_BASE_URL);
  } catch {
    throw new Error('The configured download host is invalid.');
  }
  if (base.protocol !== 'https:' && base.protocol !== 'http:') {
    throw new Error('The configured download host is invalid.');
  }
  return base;
}

function trustedBlobUrl(rawUrl: string, expectedName: string): string {
  const base = trustedApiBase();
  let url: URL;
  try {
    url = new URL(rawUrl, base);
  } catch {
    throw new Error('Stride blocked an unsafe download location.');
  }
  if (url.origin !== base.origin || url.protocol !== base.protocol || url.pathname !== '/artifacts/blob' || url.hash) {
    throw new Error('Stride blocked an unsafe download location.');
  }
  const keys = Array.from(url.searchParams.keys()).sort();
  if (keys.join(',') !== 'name,ref'
      || url.searchParams.getAll('name').length !== 1
      || url.searchParams.getAll('ref').length !== 1
      || url.searchParams.get('name') !== expectedName
      || !blobRefPattern.test(url.searchParams.get('ref') ?? '')) {
    throw new Error('The PDF download is unavailable.');
  }
  return url.toString();
}

function validateDownloadedFile(file: File): void {
  if (!file.exists || file.size <= 0) throw new Error('Stride received an empty download. Please try again.');
  if (file.size > maxStudioDownloadBytes) {
    file.delete();
    throw new Error('This export is too large to open safely on this device.');
  }
}

async function downloadPDF(sessionToken: string, request: StudioDownloadRequest): Promise<File> {
  if (!request.downloadUrl) throw new Error('The PDF download is unavailable.');
  const destination = cacheDestination(request, 'application/pdf');
  let file: File;
  try {
    file = await File.downloadFileAsync(trustedBlobUrl(request.downloadUrl, request.fileName), destination, {
      headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken, { Accept: 'application/pdf' }),
      idempotent: true,
    });
  } catch (caught) {
    if (caught instanceof Error && /unsafe download location|download host is invalid|PDF download is unavailable/u.test(caught.message)) throw caught;
    throw new Error('PDF download failed. Check your connection and try again.');
  }
  validateDownloadedFile(file);
  return file;
}

async function downloadPowerPoint(sessionToken: string, request: StudioDownloadRequest): Promise<File> {
  if (request.kind !== 'deck' || !request.expectedVersion || !request.sceneRef) {
    throw new Error('The PowerPoint export request is incomplete.');
  }
  const base = trustedApiBase();
  const exportUrl = new URL('/artifacts/export-pptx', base);
  if (exportUrl.origin !== base.origin || exportUrl.protocol !== base.protocol) {
    throw new Error('The PowerPoint export host is invalid.');
  }
  let response: Response;
  try {
    response = await fetch(exportUrl.toString(), {
      method: 'POST',
      headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken, {
        Accept: pptxMime,
        'Content-Type': 'application/json',
      }),
      body: JSON.stringify({
        artifactId: request.artifactId,
        expectedVersion: request.expectedVersion,
        sceneRef: request.sceneRef,
      }),
    });
  } catch {
    throw new Error('PowerPoint download failed. Check your connection and try again.');
  }
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw new Error(safeServerError(payload, 'PowerPoint export failed. Please reopen the deck and try again.'));
  }
  if (normalizedContentType(response.headers.get('content-type')) !== pptxMime) {
    throw new Error('Stride received an invalid PowerPoint response.');
  }
  const declaredLength = Number(response.headers.get('content-length') ?? 0);
  if (Number.isFinite(declaredLength) && declaredLength > maxStudioDownloadBytes) {
    throw new Error('This export is too large to open safely on this device.');
  }
  let bytes: Uint8Array;
  try {
    bytes = new Uint8Array(await response.arrayBuffer());
  } catch {
    throw new Error('PowerPoint download was interrupted. Please try again.');
  }
  if (bytes.byteLength < 4 || bytes.byteLength > maxStudioDownloadBytes
      || bytes[0] !== 0x50 || bytes[1] !== 0x4b || bytes[2] !== 0x03 || bytes[3] !== 0x04) {
    throw new Error('Stride received an invalid PowerPoint file.');
  }
  const destination = cacheDestination(request, pptxMime);
  try {
    destination.create({ intermediates: true, overwrite: true });
    destination.write(bytes);
  } catch {
    throw new Error('Stride could not save the PowerPoint on this device.');
  }
  validateDownloadedFile(destination);
  return destination;
}

/** Authenticated server transfer followed by the native iOS/iPadOS share/save sheet. */
export async function shareStudioDownload(
  sessionToken: string,
  request: StudioDownloadRequest,
): Promise<void> {
  if (!sessionToken.trim()) throw new Error('Your session expired. Sign in again to download this file.');
  if (!(await Sharing.isAvailableAsync())) throw new Error('File sharing is not available on this device.');
  const file = request.format === 'pptx'
    ? await downloadPowerPoint(sessionToken, request)
    : await downloadPDF(sessionToken, request);
  try {
    await Sharing.shareAsync(file.uri, {
      mimeType: request.format === 'pptx' ? pptxMime : 'application/pdf',
      dialogTitle: `Share or save ${request.fileName}`,
    });
  } catch {
    throw new Error('This device could not open the share or save options. Please try again.');
  }
}
