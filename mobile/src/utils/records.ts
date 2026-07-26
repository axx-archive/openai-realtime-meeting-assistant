export type UnknownRecord = Record<string, unknown>;

export function asRecord(value: unknown): UnknownRecord {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as UnknownRecord)
    : {};
}

export function firstString(record: UnknownRecord, keys: string[]): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'string' && value.trim()) return value.trim();
    if (typeof value === 'number') return String(value);
  }
  return '';
}

export function firstArray(value: unknown, keys: string[] = []): unknown[] {
  if (Array.isArray(value)) return value;
  const record = asRecord(value);
  for (const key of keys) {
    if (Array.isArray(record[key])) return record[key] as unknown[];
  }
  for (const nested of Object.values(record)) {
    if (Array.isArray(nested)) return nested;
    if (nested && typeof nested === 'object') {
      const candidate = firstArray(nested, keys);
      if (candidate.length) return candidate;
    }
  }
  return [];
}

export function displayTitle(value: unknown, fallback = 'Untitled'): string {
  const record = asRecord(value);
  return (
    firstString(record, ['title', 'name', 'label', 'filename', 'fileName', 'subject']) ||
    firstString(record, ['kind', 'type', 'id']) ||
    fallback
  );
}

export function displaySubtitle(value: unknown): string {
  const record = asRecord(value);
  const title = displayTitle(value, '');
  const text = firstString(record, ['summary', 'description', 'preview', 'notes', 'text', 'answer']);
  return text === title ? '' : text;
}

export function displayMeta(value: unknown): string {
  const record = asRecord(value);
  return [
    firstString(record, ['kind', 'type', 'status', 'stage']),
    firstString(record, ['updatedAt', 'createdAt', 'startedAt', 'date']),
  ]
    .filter(Boolean)
    .join(' · ');
}
