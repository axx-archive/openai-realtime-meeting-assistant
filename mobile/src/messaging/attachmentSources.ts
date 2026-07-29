export const maxMessageAttachments = 6;
export const maxAttachmentBytes = 25 * 1024 * 1024;

const supportedMimeByExtension: Record<string, string> = {
  gif: 'image/gif',
  jpeg: 'image/jpeg',
  jpg: 'image/jpeg',
  pdf: 'application/pdf',
  png: 'image/png',
  webp: 'image/webp',
};

const extensionByMime: Record<string, string> = {
  'application/pdf': 'pdf',
  'image/gif': 'gif',
  'image/jpeg': 'jpg',
  'image/png': 'png',
  'image/webp': 'webp',
};

export type AttachmentAssetInput = {
  uri: string;
  name?: string | null;
  mime?: string | null;
  size?: number | null;
};

export type PreparedAttachmentAsset = {
  uri: string;
  name: string;
  mime: string;
  size?: number;
};

export type RejectedAttachmentAsset = {
  name: string;
  reason: string;
};

export type PreparedAttachmentBatch = {
  accepted: PreparedAttachmentAsset[];
  rejected: RejectedAttachmentAsset[];
  overflowCount: number;
};

function normalizedMime(value: string | null | undefined): string {
  return String(value ?? '').split(';', 1)[0].trim().toLowerCase();
}

function pathExtension(value: string): string {
  const clean = value.split(/[?#]/, 1)[0];
  const leaf = clean.slice(clean.lastIndexOf('/') + 1);
  const dot = leaf.lastIndexOf('.');
  return dot >= 0 ? leaf.slice(dot + 1).toLowerCase() : '';
}

function mimeForAsset(asset: AttachmentAssetInput): string {
  // The picker URI describes the bytes actually handed to the app. On iOS a
  // HEIC library asset may be exported to a cache URI ending in .jpg while the
  // original fileName still ends in .HEIC, so the URI wins when recognized.
  const fromUri = supportedMimeByExtension[pathExtension(asset.uri)];
  if (fromUri) return fromUri;
  const declared = normalizedMime(asset.mime);
  if (extensionByMime[declared]) return declared;
  return supportedMimeByExtension[pathExtension(String(asset.name ?? ''))] ?? '';
}

function safeBaseName(value: string): string {
  const leaf = value.trim().split(/[\\/]/).pop() || 'attachment';
  const dot = leaf.lastIndexOf('.');
  return (dot > 0 ? leaf.slice(0, dot) : leaf)
    .replace(/[\x00-\x1f\x7f:*?"<>|]/g, '-')
    .replace(/^\.+/, '')
    .slice(0, 110) || 'attachment';
}

function normalizedFileName(asset: AttachmentAssetInput, mime: string): string {
  const original = String(asset.name ?? '').trim();
  const fallback = asset.uri.split(/[?#]/, 1)[0].split('/').pop() || 'attachment';
  return `${safeBaseName(original || fallback)}.${extensionByMime[mime]}`;
}

export function prepareAttachmentBatch(
  assets: readonly AttachmentAssetInput[],
  availableSlots: number,
): PreparedAttachmentBatch {
  const accepted: PreparedAttachmentAsset[] = [];
  const rejected: RejectedAttachmentAsset[] = [];
  const limit = Math.max(0, Math.floor(availableSlots));
  const selected = assets.slice(0, limit);

  for (const asset of selected) {
    const label = String(asset.name ?? '').trim() || 'That file';
    const uri = String(asset.uri ?? '').trim();
    if (!uri) {
      rejected.push({ name: label, reason: 'has no readable file data' });
      continue;
    }
    const mime = mimeForAsset(asset);
    if (!mime) {
      rejected.push({ name: label, reason: 'must be PNG, JPEG, WebP, GIF, or PDF' });
      continue;
    }
    const size = Number(asset.size ?? 0);
    if (Number.isFinite(size) && size > maxAttachmentBytes) {
      rejected.push({ name: label, reason: 'is larger than 25 MB' });
      continue;
    }
    accepted.push({
      uri,
      name: normalizedFileName(asset, mime),
      mime,
      ...(Number.isFinite(size) && size > 0 ? { size } : {}),
    });
  }

  return {
    accepted,
    rejected,
    overflowCount: Math.max(0, assets.length - limit),
  };
}

export function attachmentBatchMessage(batch: PreparedAttachmentBatch): string {
  const messages = batch.rejected.map((file) => `${file.name} ${file.reason}.`);
  if (batch.overflowCount > 0) {
    messages.push(`${batch.overflowCount} more ${batch.overflowCount === 1 ? 'file was' : 'files were'} not added — messages can carry up to six attachments.`);
  }
  return messages.join(' ');
}
