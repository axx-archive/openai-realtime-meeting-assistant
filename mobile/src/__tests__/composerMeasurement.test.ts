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

test('composer expands from wrapped text even when native iOS reports its pinned frame', () => {
  const width = 240;
  assert.equal(composerHeight('short line', compactComposerHeight, expandedComposerMaxHeight, width), compactComposerHeight);
  assert.equal(composerHeight('x'.repeat(90), compactComposerHeight, expandedComposerMaxHeight, width), 66);
  assert.equal(composerHeight('one\ntwo\nthree\nfour', compactComposerHeight, expandedComposerMaxHeight, width), 88);
  assert.equal(composerHeight('', 120, expandedComposerMaxHeight, width), compactComposerHeight);
});
