export const STRIDE_MEMORY_IMPORT_MAX_RAW_BYTES = 128 * 1024;
export const STRIDE_MEMORY_IMPORT_MAX_NORMALIZED_BYTES = 96 * 1024;
export const STRIDE_MEMORY_IMPORT_MAX_ENTRIES = 200;

export type ImportedMemory = { category: string; date: string; value: string };
export type ParsedMemoryImport = { entries: ImportedMemory[]; errors: string[] };

const categories = new Set(['instructions', 'identity', 'career', 'projects', 'preferences']);

export function utf8ByteLength(value: string): number {
  let bytes = 0;
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    bytes += codePoint <= 0x7f ? 1 : codePoint <= 0x7ff ? 2 : codePoint <= 0xffff ? 3 : 4;
  }
  return bytes;
}

function normalizeImportValue(value: string): string {
  return value
    .normalize('NFC')
    .replace(/\r\n?/g, '\n')
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, '')
    .split('\n')
    .map((line) => line.trim())
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

export function parseSTRIDEMemoryImport(raw: string): ParsedMemoryImport {
  const original = String(raw ?? '').trim();
  if (!original) return { entries: [], errors: [] };
  if (utf8ByteLength(original) > STRIDE_MEMORY_IMPORT_MAX_RAW_BYTES) {
    return { entries: [], errors: ['This export is larger than 128 KiB. Import it in two parts.'] };
  }
  const fenced = original.match(/```(?:[a-z0-9_-]+)?\s*\n([\s\S]*?)```/i);
  const source = fenced?.[1] ?? original;
  const headingPattern = /^(?:#{1,6}\s*)?(?:\d+\.\s*)?\*{0,2}(instructions|identity|career|projects|preferences)\*{0,2}:?\s*$/i;
  const entryPattern = /^\[(\d{4}-\d{2}-\d{2}|unknown)\]\s*-\s*(.*)$/i;
  const draft: ImportedMemory[] = [];
  const errors: string[] = [];
  let category = '';
  let current: ImportedMemory | null = null;
  let invalidStructure = false;

  for (const rawLine of source.split('\n')) {
    const line = rawLine.trim();
    if (!line || line === '```') continue;
    if (/^(?:export\s+)?complete(?:ness)?\s*:?/i.test(line) || /^more memories (?:remain|are available)/i.test(line)) continue;
    const heading = line.match(headingPattern);
    if (heading) {
      category = heading[1].toLowerCase();
      current = null;
      continue;
    }
    const candidate = line.replace(/^[-*]\s+/, '');
    const entry = candidate.match(entryPattern);
    if (entry && category && categories.has(category)) {
      current = { category, date: entry[1].toLowerCase(), value: entry[2] };
      draft.push(current);
      continue;
    }
    if (current) {
      current.value += `\n${rawLine}`;
      continue;
    }
    invalidStructure = true;
  }

  const entries: ImportedMemory[] = [];
  const seen = new Set<string>();
  let normalizedBytes = 0;
  for (const item of draft) {
    const value = normalizeImportValue(item.value);
    if (!value) continue;
    const entryBytes = utf8ByteLength(`[${item.date}] ${value}`);
    const key = `${item.category}\u0000${item.date}\u0000${value.toLocaleLowerCase()}`;
    if (seen.has(key)) continue;
    seen.add(key);
    normalizedBytes += entryBytes;
    if (normalizedBytes > STRIDE_MEMORY_IMPORT_MAX_NORMALIZED_BYTES) {
      errors.push('This export contains more than 96 KiB of memory. Import it in two parts.');
      break;
    }
    entries.push({ ...item, value });
    if (entries.length > STRIDE_MEMORY_IMPORT_MAX_ENTRIES) {
      errors.push('One import can contain up to 200 memories. Import the rest in a second pass.');
      entries.length = STRIDE_MEMORY_IMPORT_MAX_ENTRIES;
      break;
    }
  }
  if (invalidStructure && entries.length === 0 && errors.length === 0) {
    errors.push('Use the five category headings and [date] - entry format shown above.');
  }
  return { entries, errors };
}
