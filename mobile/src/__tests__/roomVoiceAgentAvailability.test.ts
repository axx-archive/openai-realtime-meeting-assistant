import assert from 'node:assert/strict';
import test from 'node:test';
import { roomVoiceAgentControlsAvailable } from '../meetings/roomVoiceAgentAvailability';

const unavailable = {
  inNativeRoom: true,
  currentScopeKey: 'session-b\u0000bonfire',
  resolvedScopeKey: 'session-b\u0000bonfire',
  scoutVoiceEnabled: false,
  specialistVoiceAvailable: false,
  controlAgentCount: 0,
  liveAgentCount: 0,
};

test('unqualified room voice agents stay out of the meeting controls', () => {
  assert.equal(roomVoiceAgentControlsAvailable(unavailable), false);
  assert.equal(roomVoiceAgentControlsAvailable({ ...unavailable, inNativeRoom: false, scoutVoiceEnabled: true }), false);
  assert.equal(roomVoiceAgentControlsAvailable({
    ...unavailable,
    resolvedScopeKey: 'session-a\u0000bonfire',
    scoutVoiceEnabled: true,
  }), false);
});

test('qualified routes and already-present agents remain manageable', () => {
  assert.equal(roomVoiceAgentControlsAvailable({ ...unavailable, scoutVoiceEnabled: true }), true);
  assert.equal(roomVoiceAgentControlsAvailable({ ...unavailable, specialistVoiceAvailable: true }), true);
  assert.equal(roomVoiceAgentControlsAvailable({ ...unavailable, controlAgentCount: 1 }), true);
  assert.equal(roomVoiceAgentControlsAvailable({
    ...unavailable,
    resolvedScopeKey: 'stale-session\u0000other-room',
    liveAgentCount: 1,
  }), true);
});
