export type PersonalRealtimeStatus =
  | 'idle'
  | 'connecting'
  | 'listening'
  | 'hearing'
  | 'thinking'
  | 'talking'
  | 'acting'
  | 'error';

export type RealtimeFunctionCall = {
  callId: string;
  name: string;
  argumentsText: string;
};

export function normalizeRealtimeSDP(value: unknown): string {
  const normalized = String(value ?? '').trim().replace(/\r\n/g, '\n').replace(/\r/g, '\n');
  if (!normalized) return '';
  return `${normalized.split('\n').map((line) => line.trimEnd()).join('\r\n')}\r\n`;
}

export function realtimeStatusForEvent(
  type: string,
  current: PersonalRealtimeStatus,
): PersonalRealtimeStatus {
  if (type === 'error') return 'error';
  if (type.includes('speech_started')) return 'hearing';
  if (type.includes('speech_stopped')) return 'thinking';
  if (type.includes('response.audio') || type.includes('output_audio_buffer.started')) return 'talking';
  if (type.includes('response.done') || type.includes('output_audio_buffer.stopped')) return 'listening';
  return current;
}

export function realtimeFunctionCalls(event: Record<string, unknown>): RealtimeFunctionCall[] {
  const type = String(event.type ?? '');
  const candidates: Array<Record<string, unknown>> = [];
  // The function call may look complete in earlier item/argument events, but
  // its owning response can still be active. Running it there allows the tool
  // result's response.create to race that response. The Realtime contract
  // makes response.done the turn boundary, so only admit calls from it.
  if (type === 'response.done' && isRecord(event.response)) {
    const status = String(event.response.status ?? '').trim().toLowerCase();
    if (['cancelled', 'incomplete', 'failed'].includes(status)) return [];
    const output = Array.isArray(event.response.output) ? event.response.output : [];
    for (const item of output) if (isRecord(item)) candidates.push(item);
  }
  return candidates.flatMap((item) => {
    if (String(item.type ?? '') !== 'function_call') return [];
    const callId = String(item.call_id ?? item.callId ?? '').trim();
    const name = String(item.name ?? '').trim();
    if (!callId || !name) return [];
    const raw = item.arguments;
    return [{
      callId,
      name,
      argumentsText: typeof raw === 'string' ? raw : JSON.stringify(raw ?? {}),
    }];
  });
}

export function transcriptFromRealtimeEvent(
  event: Record<string, unknown>,
): { role: 'user' | 'assistant'; text: string } | null {
  const type = String(event.type ?? '');
  const text = String(event.transcript ?? event.text ?? '').trim();
  if (!text) return null;
  if (type === 'conversation.item.input_audio_transcription.completed') {
    return { role: 'user', text };
  }
  if (type.includes('output_audio_transcript.done') || type === 'response.audio_transcript.done') {
    return { role: 'assistant', text };
  }
  return null;
}

export function audioLevelFromStats(reports: unknown): number {
  let level = 0;
  const visit = (report: unknown) => {
    if (!isRecord(report)) return;
    const kind = String(report.kind ?? report.mediaType ?? '');
    const type = String(report.type ?? '');
    if (kind !== 'audio' || !['media-source', 'inbound-rtp', 'track'].includes(type)) return;
    const candidate = Number(report.audioLevel);
    if (Number.isFinite(candidate)) level = Math.max(level, Math.min(1, Math.max(0, candidate)));
  };
  if (reports instanceof Map) reports.forEach(visit);
  else if (Array.isArray(reports)) reports.forEach(visit);
  else if (isRecord(reports)) Object.values(reports).forEach(visit);
  return level;
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}
