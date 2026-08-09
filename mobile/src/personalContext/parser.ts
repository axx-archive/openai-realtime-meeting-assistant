import type { StridePersonalContextExport, StridePersonalContextSource } from '../api/types';

const SOURCE_KEYS = new Set(['personId', 'sourceId', 'revision', 'kind', 'body', 'bodyDigest', 'consentRevision', 'updatedAt']);
const EXPORT_KEYS = new Set(['personId', 'exportedAt', 'sources', 'manifestDigest']);
const identifier = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$/;
const digest = /^[a-f0-9]{64}$/;

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function exactKeys(value: Record<string, unknown>, keys: Set<string>): boolean {
  const actual = Object.keys(value);
  return actual.length === keys.size && actual.every((key) => keys.has(key));
}

function timestamp(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 64 && !Number.isNaN(Date.parse(value));
}

export function parseStridePersonalContextSource(value: unknown): StridePersonalContextSource {
  const source = record(value);
  if (!source || !exactKeys(source, SOURCE_KEYS) ||
    typeof source.personId !== 'string' || !identifier.test(source.personId) ||
    typeof source.sourceId !== 'string' || !identifier.test(source.sourceId) ||
    !Number.isSafeInteger(source.revision) || Number(source.revision) < 1 ||
    !['preference', 'reflection', 'correction'].includes(String(source.kind)) ||
    typeof source.body !== 'string' || new TextEncoder().encode(source.body).byteLength > 16_384 ||
    typeof source.bodyDigest !== 'string' || !digest.test(source.bodyDigest) ||
    !Number.isSafeInteger(source.consentRevision) || Number(source.consentRevision) < 1 ||
    !timestamp(source.updatedAt)) {
    throw new Error('Invalid personal context response');
  }
  return source as StridePersonalContextSource;
}

export function parseStridePersonalContextSources(value: unknown): StridePersonalContextSource[] {
  if (!Array.isArray(value) || value.length > 200) throw new Error('Invalid personal context response');
  const sources = value.map(parseStridePersonalContextSource);
  if (new Set(sources.map((source) => source.sourceId)).size !== sources.length) throw new Error('Invalid personal context response');
  return sources;
}

export function parseStridePersonalContextExport(value: unknown): StridePersonalContextExport {
  const result = record(value);
  if (!result || !exactKeys(result, EXPORT_KEYS) ||
    typeof result.personId !== 'string' || !identifier.test(result.personId) ||
    !timestamp(result.exportedAt) ||
    typeof result.manifestDigest !== 'string' || !digest.test(result.manifestDigest)) {
    throw new Error('Invalid personal context export');
  }
  return { personId: result.personId, exportedAt: result.exportedAt, sources: parseStridePersonalContextSources(result.sources), manifestDigest: result.manifestDigest };
}
