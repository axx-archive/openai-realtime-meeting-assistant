import type { MemoryInspectItem } from '../api/types';

export const memoryKinds = [
  { id: '', label: 'All' }, { id: 'decision', label: 'Decisions' },
  { id: 'note', label: 'Notes' }, { id: 'work_result', label: 'Work results' },
  { id: 'narrative', label: 'Storylines' }, { id: 'user_profile', label: 'About you' },
  { id: 'ledger', label: 'Records' },
];
export function memoryKindLabel(kind: string) { return memoryKinds.find((item) => item.id === kind)?.label ?? 'Memory'; }

/** IDs identify the inspected record; generated answers must still verify access and cite their sources. */
export function memoryQuestion(value: string, item?: MemoryInspectItem | null): string {
  const question = value.trim();
  if (!question) return '';
  const context = item ? `\n\nI am looking at memory record ${item.id}: ${item.title}.\nSource references: ${item.provenance.map((source) => `${source.type}:${source.id}`).join(', ') || 'none attached'}.` : '';
  return `${question}${context}\n\nUse the company sources I am authorized to read. Cite the original conversations, meetings, or work where possible. Distinguish recorded facts from inference and say when evidence is missing or conflicting.`;
}

export type MemorySourceTarget = { kind: 'meeting'; id: string } | { kind: 'thread'; id: string; messageId?: string } | { kind: 'person'; id: string } | { kind: 'decision'; id: string };
export function memorySourceTarget(item: MemoryInspectItem, source: MemoryInspectItem['provenance'][number]): MemorySourceTarget | null {
  if (!source.id) return null;
  if (source.type === 'meeting' || source.type === 'person' || source.type === 'decision') return { kind: source.type, id: source.id };
  if (source.type === 'thread') return { kind: 'thread', id: source.id, messageId: item.provenance.find((entry) => entry.type === 'message')?.id };
  return null;
}
