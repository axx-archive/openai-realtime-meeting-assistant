import type { ArtifactDispositionRef } from '../api/types';

let fallbackSequence = 0;

export function createDispositionOperationId(action: 'open' | 'save' | 'discard'): string {
  const randomUUID = globalThis.crypto?.randomUUID?.bind(globalThis.crypto);
  if (randomUUID) return `mobile-${action}-${randomUUID()}`;
  fallbackSequence += 1;
  return `mobile-${action}-${Date.now().toString(36)}-${fallbackSequence.toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

export function validDispositionRef(value: unknown): value is ArtifactDispositionRef {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const ref = value as Partial<ArtifactDispositionRef>;
  const digest = /^[0-9a-f]{64}$/u;
  return Boolean(
    ref.tenantId
    && ref.artifactId
    && Number.isInteger(ref.contentRevision)
    && Number(ref.contentRevision) > 0
    && digest.test(String(ref.contentDigest ?? ''))
    && Number.isInteger(ref.aclVersion)
    && Number(ref.aclVersion) > 0
    && digest.test(String(ref.audienceDigest ?? '')),
  );
}
