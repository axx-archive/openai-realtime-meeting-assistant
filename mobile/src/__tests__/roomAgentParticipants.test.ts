import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { roomAgentParticipantsFromPayload } from '../realtime/roomAgentParticipants';

const here = path.dirname(fileURLToPath(import.meta.url));

describe('room agent participants', () => {
  it('accepts only exact server projections and preserves agent lifecycle state', () => {
    assert.deepEqual(roomAgentParticipantsFromPayload(JSON.stringify([{
      id: 'scout',
      name: 'Scout',
      kind: 'scout',
      color: '#FF6B35',
      status: 'ready',
      voiceState: 'talking',
      invitationId: 'invite-1',
      invitedAt: '2026-08-02T12:00:00Z',
      providerSessionStarted: true,
    }])), [{
      id: 'scout',
      name: 'Scout',
      kind: 'scout',
      color: '#FF6B35',
      status: 'ready',
      voiceState: 'talking',
      invitationId: 'invite-1',
      invitedAt: '2026-08-02T12:00:00Z',
      invitedBy: undefined,
      model: undefined,
      providerSessionStarted: true,
    }]);
    assert.deepEqual(roomAgentParticipantsFromPayload([{ id: 'scout', name: 'Scout' }, null, 'Scout']), []);
  });

  it('keeps agents out of the human video grid and renders a compact live waveform bench', () => {
    const room = fs.readFileSync(path.resolve(here, '../screens/RoomScreen.tsx'), 'utf8');
    const sheet = fs.readFileSync(path.resolve(here, '../components/RoomSpecialistsSheet.tsx'), 'utf8');
    assert.match(room, /const RoomAgentBench = memo/);
    assert.match(room, /AGENTS IN THE ROOM/);
    assert.match(room, /<AgentSpeakingWaveform color=\{item\.color\} mini speaking=\{speaking\}/);
    assert.match(room, /return people;/);
    assert.doesNotMatch(room, /agent:\$\{agent\.id\}:\$\{agent\.invitationId\}/);
    assert.doesNotMatch(room, /<StrideCradle/);
    assert.match(room, /item\.voiceState === 'talking'/);
    assert.match(sheet, /Invite Scout/);
    assert.match(sheet, /Dismiss Scout/);
    assert.match(sheet, /Meeting transcription remains independent/);
  });
});
