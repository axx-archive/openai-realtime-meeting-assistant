import assert from 'node:assert/strict';
import test from 'node:test';

import {
  safeWorkProgressNote,
  workActivityPillLabel,
  workCustomerPhase,
  workCustomerPhases,
  workFamilyLabel,
  workHasDecisionCard,
  workNeedsInput,
  workPhaseLabel,
  workProgressPresentation,
} from '../messaging/workPresentation';

/**
 * Work families come from the server-owned process/result contract, with mode
 * as the compatibility fallback, and are never inferred from the prompt.
 *
 * This prevents prompt copy or a transient Research worker from relabeling the
 * final authored output.
 */
test('work families come from the server output contract, never inferred from query', () => {
  // Ordinary ungoverned work keeps the server-provided mode fallback.
  assert.equal(workFamilyLabel({ query: 'Create a polished 10-slide pitch deck', mode: 'deck' }), 'Presentation');
  assert.equal(workFamilyLabel({ query: 'Build a five-year financial model', mode: 'spreadsheet' }), 'Financial model');
  assert.equal(workFamilyLabel({ query: 'Design a new hero image', mode: 'design' }), 'Design');
  assert.equal(workFamilyLabel({ query: 'Research the market and cite sources', mode: 'research' }), 'Research');
  assert.equal(workFamilyLabel({ query: 'Draft an investor memo', mode: 'document' }), 'Document');
  assert.equal(workFamilyLabel({ query: 'Turn the meeting into a recap and decision log', mode: 'recap' }), 'Meeting recap');
  assert.equal(workFamilyLabel({ query: 'Revise this pitch deck with a stronger opening', mode: 'revision' }), 'Revision');
  assert.equal(workFamilyLabel({ query: 'Schedule a weekly market brief', mode: 'scheduled' }), 'Scheduled work');
  assert.equal(workFamilyLabel({ query: 'Prepare a repository implementation handoff', mode: 'build' }), 'Build');
  assert.equal(workFamilyLabel({ query: 'Assemble an investor package with a deck and financial model', mode: 'package' }), 'Mixed package');
  assert.equal(workFamilyLabel({ query: 'Build a source-bound market share chart', mode: 'chart' }), 'Data visualization');
  assert.equal(workFamilyLabel({ query: 'Create a launch project plan and task board', mode: 'plan' }), 'Project plan');
  // Generic mode='goal' returns stable 'Work', NOT query-inferred family
  assert.equal(workFamilyLabel({ query: 'Help me think this through', mode: 'goal' }), 'Work');
  assert.equal(workFamilyLabel({ query: 'Create a polished pitch deck', mode: 'goal' }), 'Work');
  assert.equal(
    workFamilyLabel({ query: 'Research the market', mode: 'research', processId: 'document_report' }),
    'Document',
    'the authored process outranks its transient research worker mode',
  );
  assert.equal(
    workFamilyLabel({ query: 'Research the market', mode: 'research', resultArtifactType: 'html_deck' }),
    'Presentation',
    'the exact result type outranks its transient research worker mode',
  );
  assert.equal(workFamilyLabel({ mode: 'research', resultArtifactType: 'report' }), 'Document');
  assert.equal(
    workFamilyLabel({ mode: 'research', outputFamily: 'Presentation' }),
    'Presentation',
    'the server-owned family outranks a transient worker mode',
  );
  assert.equal(
    workFamilyLabel({ mode: 'research', processId: 'document_report', outputFamily: 'Presentation' }),
    'Document',
    'the governed process outranks a stale projected family',
  );
  assert.equal(
    workFamilyLabel({ mode: 'research', outputFamily: 'presentation' }),
    'Research',
    'unknown or non-canonical families fail closed to the compatibility mode',
  );
  assert.equal(workFamilyLabel({ mode: 'research', outputFamily: '__proto__' }), 'Research');
});

test('work phases translate server stages into a small stable product grammar', () => {
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'identify_goal' }), 'Understanding');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'source_research' }), 'Gathering evidence');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'ship_deck' }), 'Delivering');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'gate_before_shipping' }), 'Verifying');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'assemble_package' }), 'Building');
  assert.equal(workPhaseLabel({ status: 'approval_required' }), 'Working');
  assert.equal(workPhaseLabel({ status: 'approval_required', checkpoint: { question: 'Choose one', options: [{ id: 'one', label: 'One' }] } }), 'Needs input');
  assert.equal(workPhaseLabel({ status: 'complete' }), 'Delivered');
});

test('activity notes keep human progress and suppress runtime/process vocabulary', () => {
  assert.equal(safeWorkProgressNote('Shaping the deck brief', 'Understanding'), 'Shaping the deck brief');
  assert.equal(safeWorkProgressNote('Gathering reliable sources', 'Working'), 'Gathering reliable sources');
  assert.equal(safeWorkProgressNote('Assembling the package', 'Building'), 'Assembling the package');
  assert.equal(safeWorkProgressNote('Packaging Studio staged process', 'Understanding'), 'Understanding');
  assert.equal(safeWorkProgressNote('Running ship_deck_v1', 'Building'), 'Building');
  assert.equal(safeWorkProgressNote('gpt-5.6-sol · max', 'Working'), 'Working');
  assert.equal(safeWorkProgressNote('run 7f3a / artifact 91be', 'Working'), 'Working');
  assert.equal(safeWorkProgressNote('tool web_search', 'Working'), 'Working');
  assert.equal(safeWorkProgressNote('Sol max reasoning', 'Working'), 'Working');
  assert.equal(safeWorkProgressNote('an unreviewed but natural-sounding update', 'Working'), 'Working');
});

test('presentation and document processes share the same four customer phases', () => {
  assert.deepEqual(
    workCustomerPhases.map(({ id, label }) => ({ id, label })),
    [
      { id: 'frame', label: 'Frame' },
      { id: 'build', label: 'Build' },
      { id: 'compose', label: 'Compose' },
      { id: 'review', label: 'Review & deliver' },
    ],
  );
  const parent = { mode: 'research', processId: 'packaging_studio', status: 'running', currentStage: 'execute', progressPercent: 38 };
  assert.equal(workFamilyLabel(parent), 'Presentation');
  assert.deepEqual(workCustomerPhase(parent), {
    id: 'build',
    label: 'Build',
    number: 2,
    count: 4,
    displayLabel: 'Phase 2/4',
  });
  assert.equal(workPhaseLabel(parent), 'Phase 2/4');
  assert.equal(workProgressPresentation(parent).percent, 38, 'the server parent percent remains canonical');
  assert.equal(
    workCustomerPhase({ ...parent, currentStage: 'ship_deck', progressPercent: 11 })?.id,
    'compose',
    'an explicit durable stage outranks the percent fallback',
  );

  const document = { mode: 'research', processId: 'document_report', status: 'running', progressPercent: 12 };
  assert.equal(workFamilyLabel(document), 'Document');
  assert.equal(workCustomerPhase({ ...document, currentStage: 'context_snapshot' })?.id, 'frame');
  assert.equal(workCustomerPhase({ ...document, currentStage: 'external_research' })?.id, 'build');
  assert.equal(workCustomerPhase({ ...document, currentStage: 'quality_gate' })?.id, 'compose');
  assert.equal(workCustomerPhase({ ...document, currentStage: 'document_jury' })?.id, 'review');
});

test('the status pill uses one concise family, phase, and percent line', () => {
  assert.equal(workActivityPillLabel({
    mode: 'research', processId: 'packaging_studio', status: 'running', progressPercent: 38,
  }), 'Presentation · Phase 2/4 · 38%');
  assert.equal(workActivityPillLabel({
    mode: 'research', processId: 'document_report', status: 'needs_attention', progressPercent: 38,
  }), 'Document · Needs attention');
  assert.equal(workActivityPillLabel({
    mode: 'goal', processId: 'document_report', status: 'complete', progressPercent: 100,
  }), 'Document · Delivered');
  assert.equal(workActivityPillLabel({
    mode: 'research', processId: 'packaging_studio', status: 'running', currentStage: 'external_research',
  }), 'Presentation · Phase 2/4');
  assert.equal(workProgressPresentation({
    mode: 'research', processId: 'packaging_studio', status: 'running', currentStage: 'external_research',
  }).percent, null, 'missing server progress remains unknown');
});

test('decision status only appears when native can render a real choice card', () => {
  const parked = { mode: 'goal', processId: 'packaging_studio', status: 'approval_required', progressPercent: 38 };
  assert.equal(workHasDecisionCard(parked), false);
  assert.equal(workNeedsInput(parked), false);
  assert.equal(workPhaseLabel(parked), 'Phase 2/4');
  const decision = {
    ...parked,
    checkpoint: {
      question: 'Which direction should shape the presentation?',
      options: [{ id: 'direction-a', label: 'Lead with the audience shift' }],
    },
  };
  assert.equal(workHasDecisionCard(decision), true);
  assert.equal(workNeedsInput(decision), true);
  assert.equal(workProgressPresentation(decision).needsInput, true);
  assert.equal(workPhaseLabel({ status: 'approval_required' }), 'Working');
});
