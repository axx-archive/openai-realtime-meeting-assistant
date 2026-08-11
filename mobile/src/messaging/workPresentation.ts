export type WorkPresentationInput = {
  query?: unknown;
  mode?: unknown;
  status?: unknown;
  currentStage?: unknown;
  progressNote?: unknown;
};

export function workFamilyLabel(work?: WorkPresentationInput | null): string {
  const description = `${String(work?.query ?? '')} ${String(work?.mode ?? '')}`.toLowerCase();
  if (/\b(schedule|scheduled|recurring|daily|weekly|monthly|every (?:day|weekday|week|month))\b/u.test(description)) return 'Scheduled work';
  if (/\b(revise|revision|redline|translate|translation|regenerate|rewrite|edit (?:this|the|my)|update (?:this|the|my))\b/u.test(description)) return 'Revision';
  if (/\b(mixed package|investor package|diligence package|fundraising package|data room package)\b/u.test(description) || /\b(research|memo|deck)\b.{0,32}\b(deck|workbook|financial model)\b/u.test(description)) return 'Mixed package';
  if (/\b(meeting recap|meeting notes|action record|decision log|transcript recap)\b/u.test(description)) return 'Meeting recap';
  if (/\b(chart|visualization|dashboard|plot|graph|data table)\b/u.test(description)) return 'Data visualization';
  if (/\b(code|implementation|repository|pull request|execution handoff|deployment handoff)\b/u.test(description)) return 'Build';
  if (/\b(project plan|task board|operating plan|roadmap|work breakdown)\b/u.test(description)) return 'Project plan';
  if (/\b(financial model|forecast|budget|valuation|cap table|cash flow|waterfall|xlsx|workbook|spreadsheet)\b/u.test(description)) return 'Financial model';
  if (/\b(deck|presentation|slides?|pitch deck|powerpoint|pptx)\b/u.test(description)) return 'Presentation';
  if (/\b(image|images|design|visual|logo|brand|mockup|illustration|render|creative)\b/u.test(description)) return 'Design';
  if (/\b(research|investigate|compare|market scan|sources?|due diligence)\b/u.test(description)) return 'Research';
  if (/\b(document|memo|brief|one-pager|report)\b/u.test(description)) return 'Document';
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
