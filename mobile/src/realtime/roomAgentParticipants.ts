import type { RoomAgentParticipant } from '../api/types';

export function roomAgentParticipantsFromPayload(data: unknown): RoomAgentParticipant[] {
  let payload = data;
  if (typeof payload === 'string') {
    try {
      payload = JSON.parse(payload) as unknown;
    } catch {
      return [];
    }
  }
  if (!Array.isArray(payload)) return [];
  return payload.flatMap((value) => {
    if (!value || typeof value !== 'object') return [];
    const record = value as Record<string, unknown>;
    const id = String(record.id ?? '').trim();
    const name = String(record.name ?? '').trim();
    const invitationId = String(record.invitationId ?? '').trim();
    if (!id || !name || !invitationId) return [];
    return [{
      id,
      name,
      kind: String(record.kind ?? 'employee').trim() || 'employee',
      color: String(record.color ?? '#FF6B35').trim() || '#FF6B35',
      status: String(record.status ?? 'starting').trim() || 'starting',
      voiceState: String(record.voiceState ?? 'starting').trim() || 'starting',
      invitationId,
      invitedAt: String(record.invitedAt ?? '').trim(),
      invitedBy: String(record.invitedBy ?? '').trim() || undefined,
      model: String(record.model ?? '').trim() || undefined,
      providerSessionStarted: record.providerSessionStarted === true,
    } satisfies RoomAgentParticipant];
  });
}
