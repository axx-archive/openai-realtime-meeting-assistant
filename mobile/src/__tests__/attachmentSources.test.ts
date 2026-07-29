import assert from 'node:assert/strict';
import test from 'node:test';

import {
  attachmentBatchMessage,
  maxAttachmentBytes,
  maxConcurrentAttachmentUploads,
  prepareAttachmentBatch,
} from '../messaging/attachmentSources';

test('attachment network work is intentionally capped below the message limit', () => {
  assert.equal(maxConcurrentAttachmentUploads, 3);
});

test('prepares picker files using a supported normalized MIME and filename', () => {
  assert.deepEqual(
    prepareAttachmentBatch([
      { uri: 'file:///cache/Quarterly%20Review.PDF', name: 'Quarterly Review.PDF', mime: 'application/pdf; charset=binary', size: 1234 },
    ], 6),
    {
      accepted: [{ uri: 'file:///cache/Quarterly%20Review.PDF', name: 'Quarterly Review.pdf', mime: 'application/pdf', size: 1234 }],
      rejected: [],
      overflowCount: 0,
    },
  );
});

test('the exported cache URI wins over an original HEIC photo name', () => {
  assert.deepEqual(
    prepareAttachmentBatch([
      { uri: 'file:///cache/selected-photo.jpg', name: 'IMG_0042.HEIC', mime: 'image/heic', size: 2048 },
    ], 6).accepted,
    [{ uri: 'file:///cache/selected-photo.jpg', name: 'IMG_0042.jpg', mime: 'image/jpeg', size: 2048 }],
  );
});

test('unsupported and oversized files are rejected without discarding valid peers', () => {
  const batch = prepareAttachmentBatch([
    { uri: 'file:///cache/notes.txt', name: 'notes.txt', mime: 'text/plain', size: 20 },
    { uri: 'file:///cache/huge.png', name: 'huge.png', mime: 'image/png', size: maxAttachmentBytes + 1 },
    { uri: 'file:///cache/photo.png', name: 'photo.png', mime: 'image/png', size: 100 },
  ], 6);
  assert.deepEqual(batch.accepted, [
    { uri: 'file:///cache/photo.png', name: 'photo.png', mime: 'image/png', size: 100 },
  ]);
  assert.equal(batch.rejected.length, 2);
  assert.match(attachmentBatchMessage(batch), /notes\.txt must be PNG/);
  assert.match(attachmentBatchMessage(batch), /huge\.png is larger than 25 MB/);
});

test('the available slot count is enforced with an explicit overflow message', () => {
  const batch = prepareAttachmentBatch([
    { uri: 'file:///one.png', name: 'one.png' },
    { uri: 'file:///two.png', name: 'two.png' },
  ], 1);
  assert.equal(batch.accepted.length, 1);
  assert.equal(batch.overflowCount, 1);
  assert.match(attachmentBatchMessage(batch), /1 more file was not added/);
});
