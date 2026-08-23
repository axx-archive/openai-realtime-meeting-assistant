import type {
  StudioProject,
  StudioProjectKind,
  StudioProjectStatus,
} from '../api/types';
import { artifactStudioKind } from '../artifacts/studioRoutes';

export type StudioProjectFilter = 'all' | StudioProjectKind;
export type StudioProjectSection = 'needs-you' | 'in-progress' | 'recent';

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

const needsYouStatuses = new Set<StudioProjectStatus>(['needs_input', 'needs_attention']);
const inProgressStatuses = new Set<StudioProjectStatus>(['queued', 'running']);
const exactDigest = /^[0-9a-f]{64}$/u;

export const studioProjectFilters: ReadonlyArray<{ id: StudioProjectFilter; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'presentation', label: 'Presentations' },
  { id: 'document', label: 'Research' },
];

export function studioProjectKindLabel(kind: StudioProjectKind): string {
  return kind === 'presentation' ? 'Presentation' : 'Research';
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

export function studioProjectSection(project: StudioProject): StudioProjectSection {
  if (needsYouStatuses.has(project.status)) return 'needs-you';
  if (inProgressStatuses.has(project.status)) return 'in-progress';
  return 'recent';
}

export function studioProjectSectionTitle(section: StudioProjectSection): string {
  switch (section) {
    case 'needs-you': return 'Needs you';
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
  for (const section of ['needs-you', 'in-progress', 'recent'] as const) {
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
