export type WorkPresentationInput = {
  query?: unknown;
  mode?: unknown;
  processId?: unknown;
  status?: unknown;
  currentStage?: unknown;
  progressPercent?: unknown;
  progressNote?: unknown;
  checkpoint?: {
    question?: unknown;
    options?: ReadonlyArray<{ id?: unknown; label?: unknown }>;
  } | null;
};

export type WorkPhasePresentation = {
  id: string;
  label: string;
  number: number;
  count: number;
  displayLabel: string;
};

type CustomerPhaseDefinition = {
  id: string;
  label: string;
  stages: ReadonlyArray<string>;
  /** Inclusive lower bound used only when the server projects the parent but not its active child stage. */
  startsAtPercent: number;
};

/**
 * The five customer-visible Packaging Studio phases used by web Activity.
 * Raw execution stages remain a durable implementation detail; native uses
 * this single map for work cards, channel continuity, and activity sheets.
 */
export const packagingStudioCustomerPhases: ReadonlyArray<CustomerPhaseDefinition> = [
  { id: 'frame', label: 'Frame the decision', stages: ['intake', 'context_snapshot'], startsAtPercent: 0 },
  { id: 'ground', label: 'Ground the recommendation', stages: ['external_research', 'evidence'], startsAtPercent: 11 },
  { id: 'story', label: 'Build the story', stages: ['red_team', 'story_architects', 'compete_architects', 'compete_judges', 'compete_choice', 'write', 'gate', 'voice', 'founder_pass', 'copy_edit'], startsAtPercent: 24 },
  { id: 'design', label: 'Design the presentation', stages: ['identity', 'imagery_direction', 'imagery_generate', 'layout_plan'], startsAtPercent: 56 },
  { id: 'finish', label: 'Finish the presentation', stages: ['ship_deck', 'draft_compile', 'ship_compile', 'slide_jury', 'quality_gate', 'ship_approval'], startsAtPercent: 79 },
] as const;

function canonicalWorkPercent(work?: WorkPresentationInput | null): number {
  const value = Number(work?.progressPercent ?? 0);
  return Number.isFinite(value) ? Math.max(0, Math.min(100, Math.round(value))) : 0;
}

export function workHasDecisionCard(work?: WorkPresentationInput | null): boolean {
  const question = String(work?.checkpoint?.question ?? '').trim();
  const options = Array.isArray(work?.checkpoint?.options) ? work.checkpoint.options : [];
  return Boolean(question && options.some((option) => String(option?.id ?? '').trim() && String(option?.label ?? '').trim()));
}

export function workNeedsInput(work?: WorkPresentationInput | null): boolean {
  const status = String(work?.status ?? '').toLowerCase();
  return ['approval_required', 'needs_input', 'parked'].includes(status) && workHasDecisionCard(work);
}

export function packagingStudioPhase(work?: WorkPresentationInput | null): WorkPhasePresentation | null {
  const stage = String(work?.currentStage ?? '').trim().toLowerCase();
  const processId = String(work?.processId ?? '').trim().toLowerCase();
  const stagePhase = packagingStudioCustomerPhases.find((phase) => phase.stages.includes(stage));
  if (processId !== 'packaging_studio') return null;
  const percent = canonicalWorkPercent(work);
  const phase = stagePhase ?? [...packagingStudioCustomerPhases].reverse().find((candidate) => percent >= candidate.startsAtPercent) ?? packagingStudioCustomerPhases[0];
  const index = packagingStudioCustomerPhases.indexOf(phase);
  return {
    id: phase.id,
    label: phase.label,
    number: index + 1,
    count: packagingStudioCustomerPhases.length,
    displayLabel: `Phase ${index + 1} of ${packagingStudioCustomerPhases.length} · ${phase.label}`,
  };
}

/**
 * Work family label — NOT inferred from query (locked plan).
 *
 * Returns a stable family based on mode only (not the user's prompt).
 * Falls back to "Work" for any unknown or untyped work.
 */
export function workFamilyLabel(work?: WorkPresentationInput | null): string {
  // Only use mode, never infer from query (the prompt)
  const mode = String(work?.mode ?? '').toLowerCase();
  const processId = String(work?.processId ?? '').toLowerCase();
  if (processId === 'packaging_studio') return 'Presentation';
  if (/schedul|recurring/u.test(mode)) return 'Scheduled work';
  if (/revis|redline|translat|regenerat/u.test(mode)) return 'Revision';
  if (/mixed|package/u.test(mode)) return 'Mixed package';
  if (/recap|meeting|transcript/u.test(mode)) return 'Meeting recap';
  if (/chart|dashboard/u.test(mode)) return 'Data visualization';
  if (/code|build|implement/u.test(mode)) return 'Build';
  if (/plan|roadmap|task/u.test(mode)) return 'Project plan';
  if (/model|workbook|spreadsheet/u.test(mode)) return 'Financial model';
  if (/deck|presentation|slides?/u.test(mode)) return 'Presentation';
  if (/design|image|creative/u.test(mode)) return 'Design';
  if (/research|investigat/u.test(mode)) return 'Research';
  if (/document|memo|brief|report/u.test(mode)) return 'Document';
  return 'Work';
}

function genericWorkPhaseLabel(work?: WorkPresentationInput | null): string {
  const status = String(work?.status ?? '').toLowerCase();
  const stage = String(work?.currentStage ?? '').toLowerCase();
  if (['complete', 'completed', 'published'].includes(status)) return 'Delivered';
  if (workNeedsInput(work)) return 'Needs input';
  if (['error', 'failed', 'needs_attention', 'rejected'].includes(status)) return 'Needs attention';
  if (/deliver|verify_goal_completed/u.test(stage)) return 'Delivering';
  if (/gate|review|verif/u.test(stage)) return 'Verifying';
  if (/(^|_)ship(_|$)/u.test(stage)) return 'Delivering';
  if (/research|source|evidence/u.test(stage)) return 'Gathering evidence';
  if (/build|draft|synth|execute|codex|assembl|compos|prepar/u.test(stage)) return 'Building';
  if (/assign|decompose|identify|goal|plan/u.test(stage)) return 'Understanding';
  return status === 'queued' ? 'Queued' : 'Working';
}

export function workPhaseLabel(work?: WorkPresentationInput | null): string {
  const status = String(work?.status ?? '').toLowerCase();
  if (['complete', 'completed', 'published'].includes(status)) return 'Delivered';
  const presentationPhase = packagingStudioPhase(work);
  return presentationPhase?.displayLabel ?? genericWorkPhaseLabel(work);
}

export function workProgressPresentation(work?: WorkPresentationInput | null) {
  const phase = packagingStudioPhase(work);
  const phaseLabel = workPhaseLabel(work);
  return {
    phase,
    phaseLabel,
    percent: canonicalWorkPercent(work),
    needsInput: workNeedsInput(work),
    progressCopy: safeWorkProgressNote(work?.progressNote, phaseLabel),
  };
}

export function safeWorkProgressNote(note: unknown, fallback: string): string {
  const value = String(note ?? '').trim();
  if (!value) return fallback;
  // Progress notes may originate beyond the presentation boundary. Admit only
  // reviewed product copy; unknown strings fail closed to the stable phase.
  const approvedNotes: Readonly<Record<string, string>> = {
    'shaping the deck brief': 'Shaping the deck brief',
    'gathering reliable sources': 'Gathering reliable sources',
    'building the first draft': 'Building the first draft',
    'checking the work': 'Checking the work',
    'preparing your deliverable': 'Preparing your deliverable',
    'waiting for your input': 'Waiting for your input',
    'ready for review': 'Ready for review',
    'saving the final deliverable': 'Saving the final deliverable',
    'drafting the document': 'Drafting the document',
    'turning the meeting into decisions': 'Turning the meeting into decisions',
    'preparing the revision': 'Preparing the revision',
    'setting the schedule': 'Setting the schedule',
    'preparing the handoff': 'Preparing the handoff',
    'assembling the package': 'Assembling the package',
    'building the visualization': 'Building the visualization',
    'mapping the plan': 'Mapping the plan',
  };
  return approvedNotes[value.toLowerCase()] ?? fallback;
}
