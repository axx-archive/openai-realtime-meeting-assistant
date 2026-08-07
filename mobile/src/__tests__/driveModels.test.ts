import assert from 'node:assert/strict';
import test from 'node:test';

import {
  activeDocumentQuery,
  completeDocumentReference,
  driveFilesForLocation,
  driveFolderChildren,
  mergeExactAttachmentGrants,
  parseDriveFiles,
  parseDriveFolders,
} from '../drive/driveModels';

test('Drive parsing keeps authorized rows and preserves folder ancestry', () => {
  const files = parseDriveFiles([
    { id: 'file-root', name: 'Launch brief.pdf', folderId: '' },
    { id: 'file-private', name: 'Do not show.pdf', authorized: false },
    { id: '', name: 'Malformed.pdf' },
  ]);
  const folders = parseDriveFolders([
    { id: 'folder-a', name: 'Launch', parentId: '' },
    { id: 'folder-b', name: 'Research', parentId: 'folder-a' },
    { id: 'folder-hidden', name: 'Hidden', authorized: false },
  ]);

  assert.deepEqual(files.map((file) => file.id), ['file-root']);
  assert.deepEqual(driveFolderChildren(folders, '').map((folder) => folder.id), ['folder-a']);
  assert.deepEqual(driveFolderChildren(folders, 'folder-a').map((folder) => folder.id), ['folder-b']);
});

test('folder filtering and document tokens remain readable', () => {
  const files = parseDriveFiles([
    { id: 'one', name: 'Launch brief.pdf', folderId: 'launch' },
    { id: 'two', name: 'Board notes.pdf', folderId: 'launch' },
    { id: 'three', name: 'Launch archive.pdf', folderId: '' },
  ]);
  assert.deepEqual(driveFilesForLocation(files, 'launch', 'brief').map((file) => file.id), ['one']);
  assert.deepEqual(activeDocumentQuery('Review #lau'), { start: 7, query: 'lau' });
  assert.equal(completeDocumentReference('Review #lau', 'Launch brief.pdf'), 'Review #Launch brief.pdf ');
});

test('only exact server-minted source grants can enter the composer', () => {
  const merged = mergeExactAttachmentGrants([], [
    { name: 'Exact.pdf', mime: 'application/pdf', ref: 'ref-1', sourceId: 'source-1', sourceRevision: 'revision-1' },
    { name: 'Filename only.pdf', mime: 'application/pdf', ref: 'ref-2' },
    { name: 'Duplicate.pdf', mime: 'application/pdf', ref: 'ref-1', sourceId: 'source-1', sourceRevision: 'revision-1' },
  ], 6);
  assert.deepEqual(merged.map((file) => file.name), ['Exact.pdf']);
});
