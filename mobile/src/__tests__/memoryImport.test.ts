import assert from 'node:assert/strict';
import test from 'node:test';
import {
  parseSTRIDEMemoryImport,
  STRIDE_MEMORY_IMPORT_MAX_NORMALIZED_BYTES,
} from '../memory/memoryImport';

test('provider exports keep wrapped entries and have no per-line ceiling', () => {
  const longValue = `A long provider memory ${'context '.repeat(4_000)}`;
  assert.ok(new TextEncoder().encode(longValue).byteLength > 24 * 1024);
  const parsed = parseSTRIDEMemoryImport(`\`\`\`\nInstructions\n[unknown] - ${longValue}\ncontinued on a wrapped line\nProjects\n[2026-08-05] - Orchid research\n\`\`\`\nExport complete: yes`);
  assert.deepEqual(parsed.errors, []);
  assert.equal(parsed.entries.length, 2);
  assert.match(parsed.entries[0].value, /continued on a wrapped line$/);
  assert.ok(new TextEncoder().encode(parsed.entries[0].value).byteLength < STRIDE_MEMORY_IMPORT_MAX_NORMALIZED_BYTES);
});

test('imports deduplicate exact entries and reject only aggregate oversize', () => {
  const duplicate = '[unknown] - Lead with the outcome';
  const parsed = parseSTRIDEMemoryImport(`Instructions\n${duplicate}\n${duplicate}`);
  assert.equal(parsed.entries.length, 1);
  assert.deepEqual(parsed.errors, []);

  const oversized = parseSTRIDEMemoryImport(`Instructions\n[unknown] - ${'x'.repeat(129 * 1024)}`);
  assert.equal(oversized.entries.length, 0);
  assert.match(oversized.errors[0], /larger than 128 KiB/);
});
