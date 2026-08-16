export type WorkPresentationInput = {
  query?: unknown;
  mode?: unknown;
  status?: unknown;
  currentStage?: unknown;
  progressNote?: unknown;
};

/**
 * Work family label — NOT inferred from query (locked plan).
 *
 * Returns a stable family based on mode only (not the user's prompt).
 * Falls back to "Work" for any unknown or untyped work.
 */
export function workFamilyLabel(work?: WorkPresentationInput | null): string {
  // Only use mode, never infer from query (the prompt)
  const mode = String(work?.mode ?? '').toLowerCase();
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

export function workPhaseLabel(work?: WorkPresentationInput | null): string {
  const status = String(work?.status ?? '').toLowerCase();
  const stage = String(work?.currentStage ?? '').toLowerCase();
  if (['complete', 'completed', 'published'].includes(status)) return 'Delivered';
  if (['approval_required', 'needs_input', 'parked'].includes(status)) return 'Needs input';
  if (['error', 'failed', 'needs_attention', 'rejected'].includes(status)) return 'Needs attention';
  if (/deliver|verify_goal_completed/u.test(stage)) return 'Delivering';
  if (/gate|review|verif/u.test(stage)) return 'Verifying';
  if (/(^|_)ship(_|$)/u.test(stage)) return 'Delivering';
  if (/research|source|evidence/u.test(stage)) return 'Gathering evidence';
  if (/build|draft|synth|execute|codex|assembl|compos|prepar/u.test(stage)) return 'Building';
  if (/assign|decompose|identify|goal|plan/u.test(stage)) return 'Understanding';
  return status === 'queued' ? 'Queued' : 'Working';
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
