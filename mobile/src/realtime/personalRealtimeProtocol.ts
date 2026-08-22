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

export const ROUTED_MESSAGE_SPEECH_INSTRUCTIONS =
  'Speak only the message string from the most recent route_conversation_turn function result, exactly as written. Do not add, omit, paraphrase, explain, or answer anything else.';

export const ROUTE_BATCH_FAILURE_MESSAGE =
  "I couldn't safely route that voice turn. Please try again.";

export const ROUTE_BATCH_FAILURE_SPEECH_INSTRUCTIONS =
  `Say exactly: "${ROUTE_BATCH_FAILURE_MESSAGE}" Do not say anything else.`;

export const PERSONAL_REALTIME_GENERIC_ERROR = 'Scout voice could not connect.';

/** Keep useful provider/native guidance while refusing markup, secrets, and dumps. */
export function safePersonalRealtimeErrorMessage(
  value: unknown,
  fallback = PERSONAL_REALTIME_GENERIC_ERROR,
): string {
  const candidate = typeof value === 'string'
    ? value.replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()
    : '';
  if (
    !candidate
    || candidate.length > 300
    || /<\/?(?:html|head|body|script|style)\b|<!doctype/i.test(candidate)
    || /\bbearer\s+[a-z0-9._~+\/-]{8,}/i.test(candidate)
    || /\b(?:authorization|api[-_ ]?key|session[-_ ]?token)\s*[:=]\s*\S+/i.test(candidate)
  ) return fallback;
  return candidate;
}

export function realtimeToolContinuationPolicy(calls: RealtimeFunctionCall[]): {
  valid: boolean;
  shouldRespond: boolean;
  instructions: string;
  failureMessage: string;
} {
  const valid = calls.length === 1
    && ['route_conversation_turn', 'do_nothing'].includes(calls[0].name);
  const shouldRespond = calls.some((call) => call.name === 'route_conversation_turn');
  return {
    valid,
    shouldRespond,
    instructions: shouldRespond
      ? (valid ? ROUTED_MESSAGE_SPEECH_INSTRUCTIONS : ROUTE_BATCH_FAILURE_SPEECH_INSTRUCTIONS)
      : '',
    failureMessage: valid ? '' : ROUTE_BATCH_FAILURE_MESSAGE,
  };
}

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
  // Every completed private turn enters the server-owned Scout route before
  // speech, so expose that short grounded lookup instead of appearing stuck.
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
