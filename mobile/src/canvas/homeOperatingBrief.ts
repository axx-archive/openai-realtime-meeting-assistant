import type { StudioProject } from '../api/types';
import { studioProjectPhaseLabel } from '../work/studioProjectModel';

export type HomeWorkItem = {
  project: StudioProject;
  label: string;
  detail: string;
};

/** A view over the existing authorized Work projection, never a new work store. */
export function homeOperatingBrief(projects: readonly StudioProject[]) {
  const judgment: HomeWorkItem[] = [];
  const moving: HomeWorkItem[] = [];
  const recent: HomeWorkItem[] = [];
  for (const project of projects) {
    const result = project.result;
    const reviewPending = result?.reviewManaged !== false && result?.canContinue === true
      && (['draft_needs_attention', 'edited_after_admission'].includes(result.qualityState ?? '')
        || result.approvalState === 'edited_after_approval');
    if (project.status === 'needs_input') {
      judgment.push({ project, label: 'Your decision', detail: project.checkpoint?.question || 'Open this work to answer the question holding it up.' });
    } else if (project.status === 'needs_attention') {
      judgment.push({ project, label: 'Needs attention', detail: project.attention?.body || project.attention?.title || 'Open this work to review what happened and decide the next step.' });
    } else if (project.feedback?.canReview && project.feedback.reviewState === 'unreviewed') {
      judgment.push({ project, label: 'Awaiting your review', detail: 'Open this exact result, then accept it or request changes.' });
    } else if (project.feedback?.reviewState === 'revision_requested') {
      judgment.push({ project, label: 'Changes requested', detail: project.feedback.currentReview?.note || 'A reviewer requested changes to this result.' });
    } else if (project.status === 'ready' && reviewPending) {
      judgment.push({ project, label: 'Review changes', detail: 'A changed or unfinished result is ready for your review.' });
    } else if (project.status === 'queued' || project.status === 'running') {
      const phase = project.phases.find((item) => item.status === 'active')?.label
        || studioProjectPhaseLabel(project.phase);
      moving.push({ project, label: project.status === 'queued' ? 'Queued' : 'In progress', detail: phase || (project.status === 'queued' ? 'Waiting to start' : 'Work is underway') });
    } else if (project.status === 'ready' && result?.artifactId) {
      recent.push({ project, label: 'Ready to open', detail: result.title || 'Open the result and its source conversation.' });
    }
  }
  return { judgment, moving, recent };
}

export function homeOperatingBriefColumns(contentWidth: number, fontScale: number): boolean {
  return Number.isFinite(contentWidth) && contentWidth >= 800
    && Number.isFinite(fontScale) && fontScale < 1.35;
}
