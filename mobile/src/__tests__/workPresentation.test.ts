import assert from 'node:assert/strict';
import test from 'node:test';

import {
  safeWorkProgressNote,
  workFamilyLabel,
  workPhaseLabel,
} from '../messaging/workPresentation';

test('recurring work families are described by deliverable instead of internal mode', () => {
  assert.equal(workFamilyLabel({ query: 'Create a polished 10-slide pitch deck', mode: 'goal' }), 'Presentation');
  assert.equal(workFamilyLabel({ query: 'Build a five-year financial model', mode: 'goal' }), 'Financial model');
  assert.equal(workFamilyLabel({ query: 'Design a new hero image', mode: 'tool_run' }), 'Design');
  assert.equal(workFamilyLabel({ query: 'Research the market and cite sources', mode: 'deep_research' }), 'Research');
  assert.equal(workFamilyLabel({ query: 'Draft an investor memo', mode: 'goal' }), 'Document');
  assert.equal(workFamilyLabel({ query: 'Turn the meeting into a recap and decision log', mode: 'goal' }), 'Meeting recap');
  assert.equal(workFamilyLabel({ query: 'Revise this pitch deck with a stronger opening', mode: 'goal' }), 'Revision');
  assert.equal(workFamilyLabel({ query: 'Schedule a weekly market brief', mode: 'goal' }), 'Scheduled work');
  assert.equal(workFamilyLabel({ query: 'Prepare a repository implementation handoff', mode: 'goal' }), 'Build');
  assert.equal(workFamilyLabel({ query: 'Assemble an investor package with a deck and financial model', mode: 'goal' }), 'Mixed package');
  assert.equal(workFamilyLabel({ query: 'Build a source-bound market share chart', mode: 'goal' }), 'Data visualization');
  assert.equal(workFamilyLabel({ query: 'Create a launch project plan and task board', mode: 'goal' }), 'Project plan');
  assert.equal(workFamilyLabel({ query: 'Help me think this through', mode: 'goal' }), 'Work');
});

test('work phases translate server stages into a small stable product grammar', () => {
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'identify_goal' }), 'Understanding');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'source_research' }), 'Gathering evidence');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'ship_deck' }), 'Delivering');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'gate_before_shipping' }), 'Verifying');
  assert.equal(workPhaseLabel({ status: 'running', currentStage: 'assemble_package' }), 'Building');
  assert.equal(workPhaseLabel({ status: 'approval_required' }), 'Needs input');
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
