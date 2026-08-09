import assert from 'node:assert/strict';
import test from 'node:test';

import {
  parseScoutMarkdown,
  truncateScoutBlocks,
} from '../messaging/scoutRichTextPresentation';

test('Scout markdown becomes native semantic blocks without visible syntax', () => {
  const blocks = parseScoutMarkdown([
    '# Decision',
    '- **Ship Friday** with @scout watching [the plan](https://example.com/plan).',
    '1. Tell the team',
    '> Keep the first pass small.',
  ].join('\n'));

  assert.deepEqual(blocks.map((block) => block.kind), ['heading', 'bullet', 'number', 'quote']);
  const visible = blocks.flatMap((block) => block.inlines.map((inline) => inline.text)).join(' ');
  assert.equal(visible.includes('#'), false);
  assert.equal(visible.includes('**'), false);
  assert.equal(visible.includes('@'), false);
  assert.equal(blocks[1].inlines.some((inline) => inline.kind === 'strong' && inline.text === 'Ship Friday'), true);
  assert.equal(blocks[1].inlines.some((inline) => inline.kind === 'mention' && inline.scout && inline.text === 'scout'), true);
  assert.equal(parseScoutMarkdown('@Insights-Analyst review this')[0].inlines.some((inline) => inline.kind === 'mention' && inline.text === 'Insights-Analyst'), true);
  assert.equal(blocks[1].inlines.some((inline) => inline.kind === 'link' && inline.url === 'https://example.com/plan'), true);
});

test('inline memory headings are promoted instead of leaking raw hashes', () => {
  const blocks = parseScoutMarkdown('Proposal — # Launch plan **Status:** ready. ## Next Ship it.');
  assert.deepEqual(blocks.map((block) => block.kind), ['paragraph', 'heading', 'heading']);
  assert.equal(blocks.flatMap((block) => block.inlines).some((inline) => inline.text.includes('#')), false);
});

test('long Scout responses truncate on a word and end with one ellipsis', () => {
  const blocks = parseScoutMarkdown(`- ${'clear decision context '.repeat(40)}`);
  const result = truncateScoutBlocks(blocks, 120);
  const visible = result.blocks.flatMap((block) => block.inlines.map((inline) => inline.text)).join('');
  assert.equal(result.truncated, true);
  assert.equal(visible.endsWith('…'), true);
  assert.equal(visible.includes('…') && visible.indexOf('…') === visible.lastIndexOf('…'), true);
  assert.ok(visible.length <= 121);
});

test('reports preserve heading levels, nested lists, task state, and rules', () => {
  const blocks = parseScoutMarkdown([
    '# Market read',
    '## Evidence',
    '- **Strong signal** from the primary source',
    '  - [x] Verified at source',
    '  - [ ] Confirm market size',
    '---',
    '1. Review the implication',
  ].join('\n'));

  assert.deepEqual(blocks.map((block) => block.kind), ['heading', 'heading', 'bullet', 'bullet', 'bullet', 'rule', 'number']);
  assert.equal(blocks[0].level, 1);
  assert.equal(blocks[1].level, 2);
  assert.equal(blocks[3].depth, 1);
  assert.equal(blocks[3].checked, true);
  assert.equal(blocks[3].marker, '✓');
  assert.equal(blocks[4].checked, false);
  assert.equal(blocks[4].marker, '○');
  assert.equal(blocks[2].inlines.some((inline) => inline.kind === 'strong'), true);
});

test('server citation evidence remains durable but invisible in native report prose', () => {
  const marker = `<!-- stride-web-citation-receipt:v1 count=5 domains=4 searches=3 response=${'a'.repeat(64)} digest=${'b'.repeat(64)} -->`;
  const blocks = parseScoutMarkdown(`# Sources\n- [Official source](https://example.com)\n${marker}`);
  const visible = blocks.flatMap((block) => block.inlines.map((inline) => inline.text)).join(' ');
  assert.equal(visible.includes('stride-web-citation-receipt'), false);
  assert.equal(visible.includes('Official source'), true);
});
