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

  it('renders agents as pinnable cradle video feeds and exposes explicit Scout controls', () => {
    const room = fs.readFileSync(path.resolve(here, '../screens/RoomScreen.tsx'), 'utf8');
    const sheet = fs.readFileSync(path.resolve(here, '../components/RoomSpecialistsSheet.tsx'), 'utf8');
    assert.match(room, /agent:\$\{agent\.id\}:\$\{agent\.invitationId\}/);
    assert.match(room, /<StrideCradle/);
    assert.match(room, /agent\.voiceState === 'talking'/);
    assert.match(sheet, /Invite Scout/);
    assert.match(sheet, /Dismiss Scout/);
    assert.match(sheet, /Meeting transcription remains independent/);
  });
});
