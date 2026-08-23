export type WorkPresentationInput = {
  query?: unknown;
  mode?: unknown;
  processId?: unknown;
  outputFamily?: unknown;
  resultArtifactType?: unknown;
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
  working: string;
  stagesByProcess: Readonly<Record<'packaging_studio' | 'document_report', ReadonlyArray<string>>>;
  /** Inclusive lower bound used only when the server projects the parent but not its active child stage. */
  startsAtPercent: number;
};

/**
 * The shared four-phase customer grammar for authored presentations and
 * documents. Raw execution stages remain a durable implementation detail;
 * native uses this one projection for status pills and Activity.
 */
export const workCustomerPhases: ReadonlyArray<CustomerPhaseDefinition> = [
  {
    id: 'frame',
    label: 'Frame',
    working: 'Scout is aligning the request, audience, and company context.',
    stagesByProcess: {
      packaging_studio: ['intake', 'context_snapshot'],
      document_report: ['context_snapshot'],
    },
    startsAtPercent: 0,
  },
  {
    id: 'build',
    label: 'Build',
    working: 'Scout is grounding the argument in the context and evidence that matter.',
    stagesByProcess: {
      packaging_studio: [
        'external_research', 'source_snapshot', 'evidence_entailment', 'evidence',
        'red_team', 'story_architects', 'compete_architects', 'compete_judges', 'compete_choice',
        'write', 'gate', 'voice', 'founder_pass', 'copy_edit',
      ],
      document_report: ['external_research', 'source_snapshot', 'evidence_entailment', 'evidence', 'story', 'write'],
    },
    startsAtPercent: 25,
  },
  {
    id: 'compose',
    label: 'Compose',
    working: 'Scout is composing the finished deliverable.',
    stagesByProcess: {
      packaging_studio: [
        'identity_candidates', 'identity_judges', 'identity_critic', 'identity',
        'imagery_direction', 'imagery_generate', 'layout_plan', 'ship_deck',
      ],
      document_report: ['quality_gate', 'draft_render'],
    },
    startsAtPercent: 50,
  },
  {
    id: 'review',
    label: 'Review & deliver',
    working: 'Scout is reviewing the exact output and preparing it for delivery.',
    stagesByProcess: {
      packaging_studio: ['draft_compile', 'slide_jury', 'quality_gate', 'ship_compile', 'ship_approval'],
      document_report: ['document_jury', 'rendered_admission', 'publish'],
    },
    startsAtPercent: 75,
  },
] as const;

/** Compatibility export for callers compiled against the earlier name. */
export const packagingStudioCustomerPhases = workCustomerPhases;

type AuthoredProcessId = 'packaging_studio' | 'document_report';

const serverOutputFamilies: ReadonlySet<string> = new Set([
  'Presentation',
  'Document',
  'Image',
  'Workbook',
  'Data table',
  'Files',
  'Work',
  'Scheduled work',
  'Revision',
  'Meeting recap',
  'Data visualization',
  'Build',
  'Project plan',
  'Financial model',
  'Design',
  'Research',
]);

function authoredProcessId(work?: WorkPresentationInput | null): AuthoredProcessId | null {
  const processId = String(work?.processId ?? '').trim().toLowerCase();
  return processId === 'packaging_studio' || processId === 'document_report'
    ? processId
    : null;
}

function canonicalWorkPercent(work?: WorkPresentationInput | null): number | null {
  const raw = work?.progressPercent;
  if (raw === undefined || raw === null || raw === '') return null;
  const value = Number(raw);
  return Number.isFinite(value) ? Math.max(0, Math.min(100, Math.round(value))) : null;
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

export function workCustomerPhase(work?: WorkPresentationInput | null): WorkPhasePresentation | null {
  const stage = String(work?.currentStage ?? '').trim().toLowerCase();
  const processId = authoredProcessId(work);
  if (!processId) return null;
  const stagePhase = workCustomerPhases.find((phase) => phase.stagesByProcess[processId].includes(stage));
  const percent = canonicalWorkPercent(work);
  const phase = stagePhase
    ?? (percent === null
      ? null
      : [...workCustomerPhases].reverse().find((candidate) => percent >= candidate.startsAtPercent))
    ?? workCustomerPhases[0];
  const index = workCustomerPhases.indexOf(phase);
  return {
    id: phase.id,
    label: phase.label,
    number: index + 1,
    count: workCustomerPhases.length,
    displayLabel: `Phase ${index + 1}/${workCustomerPhases.length}`,
  };
}

/** Compatibility alias for the earlier presentation-only helper. */
export const packagingStudioPhase = workCustomerPhase;

/**
 * Work family label — NOT inferred from query (locked plan).
 *
 * Process and exact result type are the server-owned output contract and must
 * outrank a transient worker mode such as Research. Falls back to mode for
 * ordinary ungoverned work, never to the user's prompt.
 */
export function workFamilyLabel(work?: WorkPresentationInput | null): string {
  const mode = String(work?.mode ?? '').trim().toLowerCase();
  const processId = authoredProcessId(work);
  const resultType = String(work?.resultArtifactType ?? '').trim().toLowerCase();
  if (processId === 'packaging_studio') return 'Presentation';
  if (processId === 'document_report') return 'Document';
  if (/^(?:html_deck|deck|presentation|slides?)$/u.test(resultType)) return 'Presentation';
  if (/^(?:markdown|document|doc|memo|brief|report|analysis)$/u.test(resultType)) return 'Document';
  if (/^(?:table|data_table|spreadsheet)$/u.test(resultType)) return 'Data';
  if (/^(?:image|generated_image|design)$/u.test(resultType)) return 'Design';
  if (/^(?:research|deep_research)$/u.test(resultType)) return 'Research';
  const outputFamily = String(work?.outputFamily ?? '').trim();
  if (serverOutputFamilies.has(outputFamily)) return outputFamily;
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
  if (['error', 'failed', 'needs_attention', 'rejected', 'blocked'].includes(status)) return 'Needs attention';
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
  if (workNeedsInput(work)) return 'Needs input';
  if (['error', 'failed', 'needs_attention', 'rejected', 'blocked'].includes(status)) return 'Needs attention';
  const customerPhase = workCustomerPhase(work);
  return customerPhase?.displayLabel ?? genericWorkPhaseLabel(work);
}

export function workProgressPresentation(work?: WorkPresentationInput | null) {
  const phase = workCustomerPhase(work);
  const phaseLabel = workPhaseLabel(work);
  const definition = phase ? workCustomerPhases.find((candidate) => candidate.id === phase.id) : null;
  const terminalCopy = ['Delivered', 'Needs input', 'Needs attention'].includes(phaseLabel);
  return {
    phase,
    phaseLabel,
    percent: canonicalWorkPercent(work),
    needsInput: workNeedsInput(work),
    progressCopy: safeWorkProgressNote(work?.progressNote, terminalCopy ? phaseLabel : definition?.working ?? phaseLabel),
  };
}

/** The one-line status grammar used by the persistent native work pill. */
export function workActivityPillLabel(work?: WorkPresentationInput | null): string {
  const family = workFamilyLabel(work);
  const phaseLabel = workPhaseLabel(work);
  const phase = workCustomerPhase(work);
  if (!phase || ['Delivered', 'Needs input', 'Needs attention'].includes(phaseLabel)) {
    return `${family} · ${phaseLabel}`;
  }
  const percent = canonicalWorkPercent(work);
  return percent === null
    ? `${family} · ${phase.displayLabel}`
    : `${family} · ${phase.displayLabel} · ${percent}%`;
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
