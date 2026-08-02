import assert from 'node:assert/strict';
import test from 'node:test';
import {
  audioLevelFromStats,
  normalizeRealtimeSDP,
  realtimeFunctionCalls,
  realtimeStatusForEvent,
  transcriptFromRealtimeEvent,
} from '../realtime/personalRealtimeProtocol';

test('normalizes SDP to the server and native WebRTC CRLF contract', () => {
  assert.equal(normalizeRealtimeSDP(' v=0\na=sendrecv\n'), 'v=0\r\na=sendrecv\r\n');
  assert.equal(normalizeRealtimeSDP('   '), '');
});

test('deduplicatable function call shapes match every server-supported Realtime event', () => {
  const direct = realtimeFunctionCalls({
    type: 'response.function_call_arguments.done',
    call_id: 'call-1',
    name: 'answer_memory_question',
    arguments: '{"question":"what changed?"}',
  });
  const completed = realtimeFunctionCalls({
    type: 'response.done',
    response: { output: [{ type: 'function_call', call_id: 'call-1', name: 'answer_memory_question', arguments: '{}' }] },
  });
  assert.deepEqual(direct[0], {
    callId: 'call-1',
    name: 'answer_memory_question',
    argumentsText: '{"question":"what changed?"}',
  });
  assert.equal(completed[0]?.callId, 'call-1');
  assert.deepEqual(realtimeFunctionCalls({
    type: 'response.done',
    response: { status: 'cancelled', output: [{ type: 'function_call', call_id: 'call-2', name: 'delete_ticket' }] },
  }), []);
});

test('provider events preserve text-primary turns and honest voice state', () => {
  assert.deepEqual(transcriptFromRealtimeEvent({
    type: 'conversation.item.input_audio_transcription.completed',
    transcript: 'What did I miss?',
  }), { role: 'user', text: 'What did I miss?' });
  assert.deepEqual(transcriptFromRealtimeEvent({
    type: 'response.output_audio_transcript.done',
    transcript: 'Two decisions were made.',
  }), { role: 'assistant', text: 'Two decisions were made.' });
  assert.equal(realtimeStatusForEvent('input_audio_buffer.speech_started', 'listening'), 'hearing');
  assert.equal(realtimeStatusForEvent('response.audio.delta', 'thinking'), 'talking');
  assert.equal(realtimeStatusForEvent('response.done', 'talking'), 'listening');
});

test('metering reads bounded live audio levels without decorative motion', () => {
  assert.equal(audioLevelFromStats(new Map([
    ['mic', { type: 'media-source', kind: 'audio', audioLevel: 0.42 }],
    ['video', { type: 'media-source', kind: 'video', audioLevel: 1 }],
  ])), 0.42);
  assert.equal(audioLevelFromStats([{ type: 'inbound-rtp', kind: 'audio', audioLevel: 4 }]), 1);
  assert.equal(audioLevelFromStats({}), 0);
});
