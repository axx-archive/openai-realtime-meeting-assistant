import test from 'node:test';
import assert from 'node:assert/strict';
import { parseMentions } from '../messaging/mentions';

/**
 * These cases mirror `chat_mentions.go`'s documented rules. The client must
 * highlight exactly what the server would notify on — a mention that renders
 * blue but never rings anyone is worse than no highlight at all.
 */

test('a plain mention is split out of the surrounding text', () => {
  const segments = parseMentions('hey @tyler can you look');
  assert.deepEqual(segments, [
    { kind: 'text', text: 'hey ' },
    { kind: 'mention', text: '@tyler', name: 'tyler', scout: false },
    { kind: 'text', text: ' can you look' },
  ]);
});

test('an email address is never a mention', () => {
  // The Go side requires a word boundary before "@" precisely so that
  // aj@shareability.com does not page a user named "shareability".
  const segments = parseMentions('mail aj@shareability.com today');
  assert.equal(segments.length, 1);
  assert.equal(segments[0].kind, 'text');
  assert.equal(segments[0].text, 'mail aj@shareability.com today');
});

test('trailing punctuation ends a mention but a longer word does not', () => {
  const punctuated = parseMentions('@tyler, ping');
  assert.equal(punctuated[0].kind, 'mention');
  assert.equal((punctuated[0] as { name: string }).name, 'tyler');

  const longer = parseMentions('@tylerish');
  assert.equal(longer[0].kind, 'mention');
  assert.equal((longer[0] as { name: string }).name, 'tylerish');
});

test('@scout is flagged apart from human mentions', () => {
  const segments = parseMentions('@scout what did we decide');
  assert.equal(segments[0].kind, 'mention');
  assert.equal((segments[0] as { scout: boolean }).scout, true);

  const human = parseMentions('@dana what did we decide');
  assert.equal((human[0] as { scout: boolean }).scout, false);
});

test('a bare @ is literal text, not an empty mention', () => {
  const segments = parseMentions('cost @ $5');
  assert.ok(segments.every((segment) => segment.kind === 'text'));
});

test('multiple mentions are all captured in order', () => {
  const names = parseMentions('@dana and @tyler and @scout')
    .filter((segment) => segment.kind === 'mention')
    .map((segment) => (segment as { name: string }).name);
  assert.deepEqual(names, ['dana', 'tyler', 'scout']);
});

test('unicode names are matched, since the Go side uses letter classes', () => {
  const segments = parseMentions('thanks @josé');
  assert.equal(segments[1].kind, 'mention');
  assert.equal((segments[1] as { name: string }).name, 'josé');
});
