export type ArtifactStudioKind = 'deck' | 'document';
export type ArtifactStudioIntent = 'edit' | 'present';

const deckKinds = /^(?:html_deck|deck|presentation|slides?)$/u;
const documentKinds = /^(?:markdown|document|doc|memo|brief)$/u;

export function artifactStudioKind(value: unknown): ArtifactStudioKind | null {
  const normalized = String(value ?? '').trim().toLowerCase();
  if (deckKinds.test(normalized)) return 'deck';
  if (documentKinds.test(normalized)) return 'document';
  return null;
}

/**
 * Editing is fail-closed. A caller must supply the exact `true` returned by
 * the authoritative deck/document read boundary; readable shared artifacts
 * otherwise open in their non-mutating presentation surface.
 */
export function artifactStudioIntent(canWrite: unknown): ArtifactStudioIntent {
  return canWrite === true ? 'edit' : 'present';
}

/**
 * Authenticated SPA destination opened inside the native OSWeb stack screen.
 * A document's non-edit intent opens the authenticated read surface. It must
 * never coerce a read-only viewer into Document Studio and expose a dead Edit.
 */
export function artifactStudioPath(
  artifactId: string,
  kind: ArtifactStudioKind,
  intent: ArtifactStudioIntent,
  expected?: { version: number; digest: string },
): string {
  const normalizedId = artifactId.trim();
  if (!normalizedId) return '';
  const mode = intent === 'present' ? (kind === 'deck' ? 'present' : 'view') : 'edit';
  const binding = expected
    ? `&version=${encodeURIComponent(String(expected.version))}&digest=${encodeURIComponent(expected.digest)}`
    : '';
  return `/studio/${kind}/${encodeURIComponent(normalizedId)}?mode=${mode}${binding}`;
}
