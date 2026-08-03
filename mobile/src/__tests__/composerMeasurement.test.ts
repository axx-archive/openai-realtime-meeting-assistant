import assert from 'node:assert/strict';
import test from 'node:test';

import {
  compactComposerHeight,
  composerHeight,
  editingComposerMaxHeight,
  expandedComposerMaxHeight,
} from '../messaging/composerMeasurement';

test('a programmatically cleared multiline draft snaps back to compact height', () => {
  assert.equal(composerHeight('line one\nline two\nline three', 96), 96);
  assert.equal(composerHeight('', 96), compactComposerHeight);
});

test('composer measurement clamps invalid, short, and oversized content', () => {
  assert.equal(composerHeight('draft', Number.NaN), compactComposerHeight);
  assert.equal(composerHeight('draft', 12), compactComposerHeight);
  assert.equal(composerHeight('draft', 400), expandedComposerMaxHeight);
  assert.equal(composerHeight('draft', 400, editingComposerMaxHeight), editingComposerMaxHeight);
});
