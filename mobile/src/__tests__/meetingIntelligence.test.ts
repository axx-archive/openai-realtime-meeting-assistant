import assert from 'node:assert/strict';
import test from 'node:test';
import {
  meetingIntelligenceReducer,
  meetingIntelligenceStatusLabel,
  parseMeetingIntelligenceSnapshot,
} from '../realtime/meetingIntelligence';

function snapshot(overrides: Record<string, unknown> = {}) {
  return {
    contract: 'meeting-intelligence-v1',
    roomId: 'office',
    meetingId: 'meeting-1',
    revision: 'revision-1',
    generatedAt: '2026-08-11T20:00:00Z',
    transcript: {
      state: 'listening', captureHighWater: 44, sequenceComplete: true,
      segmentCount: 12, lastSegmentId: 'transcript-44', lastCapturedAt: '2026-08-11T19:59:30Z',
    },
    notes: {
      state: 'current', revision: 'digest-3', updatedAt: '2026-08-11T20:00:00Z',
      groundedThrough: '2026-08-11T19:59:30Z', analysisCaptureHighWater: 44, coverage: 'full',
    },
    scout: { state: 'ready', groundedThrough: '2026-08-11T19:59:30Z', sourceCount: 3 },
    recap: {
      title: 'Launch readiness',
      topics: [{ text: 'Meeting intelligence', sourceId: 'transcript-44', at: '2026-08-11T19:59:30Z' }],
      decisions: [{ text: 'Use one evolving recap', owner: 'AJ', status: 'ratified', sourceId: 'transcript-44' }],
      actions: [{ text: 'Wire the native sheet', owner: 'Scout', status: 'open', sourceId: 'transcript-44' }],
      openQuestions: [], risks: [], themes: ['Meeting memory'], sourceCount: 3,
    },
    ...overrides,
  };
}

test('parses an exact grounded meeting intelligence snapshot', () => {
  const parsed = parseMeetingIntelligenceSnapshot(snapshot());
  assert.equal(parsed?.meetingId, 'meeting-1');
  assert.equal(parsed?.recap?.decisions[0]?.sourceId, 'transcript-44');
  assert.equal(meetingIntelligenceStatusLabel(parsed), 'Listening · Notes current');
});

test('ignores malformed or regressing snapshots without erasing current notes', () => {
  const current = parseMeetingIntelligenceSnapshot(snapshot());
  assert.ok(current);
  assert.equal(meetingIntelligenceReducer(current, { type: 'snapshot', payload: { contract: 'meeting-intelligence-v1' } }), current);
  const regressing = snapshot({
    revision: 'revision-old',
    generatedAt: '2026-08-11T20:00:01Z',
    transcript: { state: 'listening', captureHighWater: 43, sequenceComplete: true, segmentCount: 11 },
  });
  assert.equal(meetingIntelligenceReducer(current, { type: 'snapshot', payload: regressing }), current);
});

test('rejects contradictory current, high-water, and Scout truth claims', () => {
  const impossibleCurrent = snapshot({
    transcript: { state: 'listening', captureHighWater: 44, sequenceComplete: false, segmentCount: 12, lastSegmentId: 'transcript-44', lastCapturedAt: '2026-08-11T19:59:30Z' },
    notes: { state: 'current', revision: 'digest-3', updatedAt: '2026-08-11T20:00:00Z', analysisCaptureHighWater: 43, coverage: 'full' },
    scout: { state: 'ready', sourceCount: 3 },
  });
  assert.equal(parseMeetingIntelligenceSnapshot(impossibleCurrent), null);
  assert.equal(parseMeetingIntelligenceSnapshot(snapshot({
    transcript: { state: 'listening', captureHighWater: 0, sequenceComplete: true, segmentCount: 0 },
    notes: { state: 'current', revision: 'digest-retained', updatedAt: '2026-08-11T20:00:00Z', analysisCaptureHighWater: 0, coverage: 'unknown' },
    scout: { state: 'ready', sourceCount: 1 },
  })), null);
  assert.equal(parseMeetingIntelligenceSnapshot(snapshot({
    notes: { state: 'catching_up', analysisCaptureHighWater: 45, coverage: 'unknown' },
    scout: { state: 'not_caught_up', sourceCount: 0 },
    recap: undefined,
  })), null);
});

test('a newer live transcript immediately makes notes and Scout catch up', () => {
  const current = parseMeetingIntelligenceSnapshot(snapshot());
  assert.ok(current);
  const progressed = meetingIntelligenceReducer(current, {
    type: 'transcript_progress',
    payload: {
      id: 'transcript-45',
      kind: 'transcript',
      text: 'AJ: Keep the freshness badge honest.',
      createdAt: '2026-08-11T20:00:00Z',
      metadata: { roomId: 'office', meetingId: 'meeting-1', captureSequence: '45' },
    },
  });
  assert.ok(progressed);
  assert.equal(progressed.transcript.captureHighWater, 45);
  assert.equal(progressed.transcript.segmentCount, 13);
  assert.equal(progressed.transcript.lastSegmentId, 'transcript-45');
  assert.equal(progressed.transcript.sequenceComplete, true);
  assert.equal(progressed.notes.state, 'catching_up');
  assert.equal(progressed.scout.state, 'not_caught_up');

  const stale = meetingIntelligenceReducer(progressed, {
    type: 'transcript_progress',
    payload: {
      id: 'transcript-44', createdAt: '2026-08-11T19:59:30Z',
      metadata: { roomId: 'office', meetingId: 'meeting-1', captureSequence: '44' },
    },
  });
  assert.equal(stale, progressed);

  const gapped = meetingIntelligenceReducer(current, {
    type: 'transcript_progress',
    payload: {
      id: 'transcript-46', createdAt: '2026-08-11T20:00:02Z',
      metadata: { roomId: 'office', meetingId: 'meeting-1', captureSequence: '46' },
    },
  });
  assert.equal(gapped?.transcript.sequenceComplete, false);
});

test('renders truthful paused and catching-up states', () => {
  const paused = parseMeetingIntelligenceSnapshot(snapshot({
    transcript: { state: 'transcript_paused', captureHighWater: 44, sequenceComplete: true, segmentCount: 12, lastSegmentId: 'transcript-44', lastCapturedAt: '2026-08-11T19:59:30Z' },
    notes: { state: 'catching_up', analysisCaptureHighWater: 43, coverage: 'partial_synthesis' },
    scout: { state: 'not_caught_up', sourceCount: 0 },
    recap: undefined,
  }));
  assert.equal(meetingIntelligenceStatusLabel(paused), 'Transcript paused');
  const catchingUp = parseMeetingIntelligenceSnapshot(snapshot({
    notes: { state: 'catching_up', analysisCaptureHighWater: 43, coverage: 'unknown' },
    scout: { state: 'not_caught_up', sourceCount: 0 },
    recap: undefined,
  }));
  assert.equal(meetingIntelligenceStatusLabel(catchingUp), 'Listening · Notes catching up');
});
