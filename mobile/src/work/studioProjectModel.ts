import type {
  StudioProject,
  StudioProjectAttention,
  StudioProjectKind,
  StudioProjectStatus,
} from '../api/types';
import { artifactStudioKind } from '../artifacts/studioRoutes';

export type StudioProjectFilter = 'all' | StudioProjectKind;
export type StudioProjectSection = 'needs-you' | 'needs-attention' | 'in-progress' | 'recent';

export type StudioProjectListRow =
  | { type: 'section'; id: `section:${StudioProjectSection}`; section: StudioProjectSection; title: string }
  | { type: 'project'; id: `project:${string}`; project: StudioProject };

export type StudioProjectOpenTarget =
  | {
      kind: 'deck';
      artifactId: string;
      artifactVersion: number;
      artifactDigest: string;
      title: string;
      desktopEditable: boolean;
      canPresent: boolean;
    }
  | {
      kind: 'document';
      artifactId: string;
      artifactVersion: number;
      artifactDigest: string;
      title: string;
      canEdit: boolean;
    };

const inProgressStatuses = new Set<StudioProjectStatus>(['queued', 'running']);
const exactDigest = /^[0-9a-f]{64}$/u;
const phaseLabels = new Map([
  ['brief', 'Brief'],
  ['build', 'Build'],
  ['polish', 'Polish'],
  ['ready', 'Ready'],
]);

export const studioProjectFilters: ReadonlyArray<{ id: StudioProjectFilter; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'presentation', label: 'Presentations' },
  { id: 'document', label: 'Research' },
];

export function studioProjectKindLabel(kind: StudioProjectKind): string {
  return ({ presentation: 'Presentation', document: 'Research', research: 'Research', image: 'Image', sheet: 'Spreadsheet', artifact: 'Artifact' })[kind] || 'Work';
}

export function studioProjectStatusLabel(status: StudioProjectStatus): string {
  switch (status) {
    case 'queued': return 'Queued';
    case 'running': return 'In progress';
    case 'needs_input': return 'Needs you';
    case 'needs_attention': return 'Needs attention';
    case 'ready': return 'Ready';
    case 'stopped': return 'Stopped';
  }
}

export function studioProjectBoundedProgress(value: unknown): number | null {
  const progress = typeof value === 'number' ? value : Number.NaN;
  if (!Number.isFinite(progress)) return null;
  return Math.max(0, Math.min(100, Math.round(progress)));
}

/** Only expose the small, customer-facing phase vocabulary in conversation. */
export function studioProjectPhaseLabel(value: unknown): string {
  const phase = typeof value === 'string' ? value.trim().toLowerCase() : '';
  return phaseLabels.get(phase) ?? '';
}

function boundedViewerCopy(value: unknown, maxLength: number): string {
  if (typeof value !== 'string') return '';
  const normalized = value.trim().replace(/\s+/gu, ' ');
  if (normalized.length <= maxLength) return normalized;
  return `${normalized.slice(0, Math.max(0, maxLength - 1)).trimEnd()}…`;
}

export function studioProjectAttentionCopy(
  attention: StudioProjectAttention | null | undefined,
  hasSource: boolean,
): { title: string; body: string; actionLabel: string } {
  return {
    title: boundedViewerCopy(attention?.title, 100) || 'This work needs attention',
    body: boundedViewerCopy(attention?.body, 240) || (hasSource
      ? 'Open the source conversation to review what happened and tell Scout how to continue.'
      : 'Scout could not finish or verify a final file. Ask Scout to continue from the original request.'),
    actionLabel: boundedViewerCopy(attention?.actionLabel, 44),
  };
}

export function studioProjectSection(project: StudioProject): StudioProjectSection {
  if (project.status === 'needs_input') return 'needs-you';
  if (project.status === 'needs_attention') return 'needs-attention';
  if (inProgressStatuses.has(project.status)) return 'in-progress';
  return 'recent';
}

export function studioProjectSectionTitle(section: StudioProjectSection): string {
  switch (section) {
    case 'needs-you': return 'Needs you';
    case 'needs-attention': return 'Needs attention';
    case 'in-progress': return 'In progress';
    case 'recent': return 'Recent';
  }
}

export function studioProjectResultIsFinal(project: StudioProject): boolean {
  const result = project.result;
  if (!result?.artifactId || project.status !== 'ready' || result.canContinue === true) return false;
  const qualityState = String(result.qualityState ?? '').trim().toLowerCase();
  return result.reviewManaged === false ? qualityState === '' : qualityState === 'admitted';
}

export function studioProjectListRows(
  projects: readonly StudioProject[],
  filter: StudioProjectFilter,
): StudioProjectListRow[] {
  const visible = filter === 'all'
    ? projects
    : projects.filter((project) => project.kind === filter);
  const rows: StudioProjectListRow[] = [];
  for (const section of ['needs-you', 'needs-attention', 'in-progress', 'recent'] as const) {
    const sectionProjects = visible.filter((project) => studioProjectSection(project) === section);
    if (sectionProjects.length === 0) continue;
    rows.push({
      type: 'section',
      id: `section:${section}`,
      section,
      title: studioProjectSectionTitle(section),
    });
    for (const project of sectionProjects) {
      rows.push({ type: 'project', id: `project:${project.id}`, project });
    }
  }
  return rows;
}

/**
 * Studio can open only the exact, capability-bound result the server returned.
 * A title, project kind, preview, or result filename can never substitute for
 * the concrete artifact family/version/digest contract.
 */
export function studioProjectOpenTarget(project: StudioProject): StudioProjectOpenTarget | null {
  const result = project.result;
  if (!result) return null;
  const artifactId = String(result.artifactId ?? '').trim();
  const artifactVersion = Number(result.version);
  const artifactDigest = String(result.digest ?? '').trim().toLowerCase();
  const exact = Boolean(artifactId)
    && Number.isSafeInteger(artifactVersion)
    && artifactVersion > 0
    && exactDigest.test(artifactDigest);
  if (!exact) return null;

  const studioKind = artifactStudioKind(result.type);
  if (project.kind === 'presentation') {
    if (studioKind !== 'deck') return null;
    return {
      kind: 'deck',
      artifactId,
      artifactVersion,
      artifactDigest,
      title: String(result.title ?? '').trim() || project.title || 'Presentation',
      desktopEditable: result.canEdit === true,
      canPresent: result.canPresent === true,
    };
  }
  if (studioKind !== 'document') return null;
  return {
    kind: 'document',
    artifactId,
    artifactVersion,
    artifactDigest,
    title: String(result.title ?? '').trim() || project.title || 'Research',
    canEdit: result.canEdit === true,
  };
}

export function studioProjectRelativeTime(value: string, now = Date.now()): string {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return '';
  const elapsedMinutes = Math.max(0, Math.floor((now - timestamp) / 60_000));
  if (elapsedMinutes < 1) return 'Now';
  if (elapsedMinutes < 60) return `${elapsedMinutes}m`;
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) return `${elapsedHours}h`;
  const elapsedDays = Math.floor(elapsedHours / 24);
  if (elapsedDays < 7) return `${elapsedDays}d`;
  return new Date(timestamp).toLocaleDateString([], { month: 'short', day: 'numeric' });
}
