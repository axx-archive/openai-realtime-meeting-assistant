import type { DriveFileRecord, DriveFolderRecord, ScoutFileAttachment } from '../api/types';

function record(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

export function parseDriveFiles(values: readonly unknown[]): DriveFileRecord[] {
  return values.flatMap((value) => {
    const row = record(value);
    const id = text(row?.id);
    const name = text(row?.name);
    if (!row || row.authorized === false || !id || !name) return [];
    return [{
      id,
      name,
      mime: text(row.mime),
      size: typeof row.size === 'number' && Number.isFinite(row.size) ? row.size : 0,
      folderId: text(row.folderId),
      artifactId: text(row.artifactId),
      origin: text(row.origin),
      downloadUrl: text(row.downloadUrl),
      canRename: row.canRename !== false,
    }];
  });
}

export function parseDriveFolders(values: readonly unknown[]): DriveFolderRecord[] {
  return values.flatMap((value) => {
    const row = record(value);
    const id = text(row?.id);
    const name = text(row?.name);
    if (!row || row.authorized === false || !id || !name) return [];
    return [{
      id,
      name,
      count: typeof row.count === 'number' && Number.isFinite(row.count) ? row.count : 0,
      parentId: text(row.parentId),
    }];
  });
}

export function driveFolderChildren(folders: readonly DriveFolderRecord[], parentId: string): DriveFolderRecord[] {
  return folders.filter((folder) => String(folder.parentId ?? '') === parentId);
}

export function driveFilesForLocation(
  files: readonly DriveFileRecord[],
  folderId: string,
  query = '',
): DriveFileRecord[] {
  const needle = query.trim().toLocaleLowerCase();
  return files.filter((file) => (
    String(file.folderId ?? '') === folderId
    && (!needle || file.name.toLocaleLowerCase().includes(needle))
  ));
}

export type ActiveDocumentQuery = { start: number; query: string };

export function activeDocumentQuery(value: string): ActiveDocumentQuery | null {
  const match = /(?:^|\s)#([\p{L}\p{N}._-]*)$/u.exec(value);
  if (!match || match.index === undefined) return null;
  const start = match.index + (match[0].startsWith('#') ? 0 : 1);
  return { start, query: String(match[1] ?? '').trimStart() };
}

export function completeDocumentReference(value: string, name: string): string {
  const active = activeDocumentQuery(value);
  const clean = name.trim().replace(/^#+/u, '');
  if (!active || !clean) return value;
  return `${value.slice(0, active.start)}#${clean} `;
}

export function mergeExactAttachmentGrants(
  current: readonly ScoutFileAttachment[],
  added: readonly ScoutFileAttachment[],
  limit: number,
): ScoutFileAttachment[] {
  const next = [...current];
  const identities = new Set(next.map((file) => `${file.sourceId ?? ''}:${file.sourceRevision ?? ''}:${file.ref}`));
  for (const file of added) {
    if (!file.ref || !file.sourceId || !file.sourceRevision) continue;
    const identity = `${file.sourceId}:${file.sourceRevision}:${file.ref}`;
    if (identities.has(identity)) continue;
    identities.add(identity);
    next.push(file);
    if (next.length >= limit) break;
  }
  return next;
}
