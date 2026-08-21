import assert from 'node:assert/strict';
import test from 'node:test';

import {
  packagingStudioCustomerPhases,
  packagingStudioPhase,
  safeWorkProgressNote,
  workFamilyLabel,
  workHasDecisionCard,
  workNeedsInput,
  workPhaseLabel,
  workProgressPresentation,
} from '../messaging/workPresentation';

/**
 * Work families come from mode, NOT inferred from query (the prompt).
 *
 * The locked plan says: "Family is not inferred from the query." This prevents
 * showing user's prompt as a family label ("Presentation" when user asked for slides).
 * Mode-based labeling is stable and doesn't leak prompt content into titles.
 */
test('work families come from mode, not inferred from query (locked plan)', () => {
  // Mode-based labeling — the family comes from the server-provided mode
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

test('Packaging Studio projects the same five customer phases as web Activity', () => {
  assert.deepEqual(
    packagingStudioCustomerPhases.map(({ id, label }) => ({ id, label })),
    [
      { id: 'frame', label: 'Frame the decision' },
      { id: 'ground', label: 'Ground the recommendation' },
      { id: 'story', label: 'Build the story' },
      { id: 'design', label: 'Design the presentation' },
      { id: 'finish', label: 'Finish the presentation' },
    ],
  );
  const parent = { mode: 'goal', processId: 'packaging_studio', status: 'running', currentStage: 'execute', progressPercent: 11 };
  assert.equal(workFamilyLabel(parent), 'Presentation');
  assert.deepEqual(packagingStudioPhase(parent), {
    id: 'ground',
    label: 'Ground the recommendation',
    number: 2,
    count: 5,
    displayLabel: 'Phase 2 of 5 · Ground the recommendation',
  });
  assert.equal(workPhaseLabel(parent), 'Phase 2 of 5 · Ground the recommendation');
  assert.equal(workProgressPresentation(parent).percent, 11, 'the server parent percent remains canonical');
  assert.equal(
    packagingStudioPhase({ ...parent, currentStage: 'ship_deck', progressPercent: 11 })?.id,
    'finish',
    'an explicit durable stage outranks the percent fallback',
  );
});

test('decision status only appears when native can render a real choice card', () => {
  const parked = { mode: 'goal', processId: 'packaging_studio', status: 'approval_required', progressPercent: 24 };
  assert.equal(workHasDecisionCard(parked), false);
  assert.equal(workNeedsInput(parked), false);
  assert.equal(workPhaseLabel(parked), 'Phase 3 of 5 · Build the story');
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
