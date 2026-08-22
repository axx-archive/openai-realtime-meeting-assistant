import assert from 'node:assert/strict';
import test from 'node:test';
import {
  audioLevelFromStats,
  normalizeRealtimeSDP,
  realtimeFunctionCalls,
  realtimeStatusForEvent,
  realtimeToolContinuationPolicy,
  transcriptFromRealtimeEvent,
} from '../realtime/personalRealtimeProtocol';

test('normalizes SDP to the server and native WebRTC CRLF contract', () => {
  assert.equal(normalizeRealtimeSDP(' v=0\na=sendrecv\n'), 'v=0\r\na=sendrecv\r\n');
  assert.equal(normalizeRealtimeSDP('   '), '');
});

test('function calls wait for response.done before continuing the Realtime turn', () => {
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
  assert.deepEqual(direct, []);
  assert.deepEqual(realtimeFunctionCalls({
    type: 'response.output_item.done',
    item: { type: 'function_call', call_id: 'call-1', name: 'answer_memory_question', arguments: '{}' },
  }), []);
  assert.equal(completed[0]?.callId, 'call-1');
  assert.deepEqual(realtimeFunctionCalls({
    type: 'response.done',
    response: { status: 'completed', output: [
      { type: 'function_call', call_id: 'call-a', name: 'answer_memory_question', arguments: '{}' },
      { type: 'function_call', call_id: 'call-b', name: 'do_nothing', arguments: '{}' },
    ] },
  }).map((call) => call.callId), ['call-a', 'call-b']);
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

test('speech_stopped truthfully shows the grounded Scout route before audio', () => {
  assert.equal(realtimeStatusForEvent('input_audio_buffer.speech_stopped', 'hearing'), 'thinking');
  assert.equal(realtimeStatusForEvent('input_audio_buffer.speech_stopped', 'listening'), 'thinking');
});

test('silence stays silent and routed turns speak only the durable message', () => {
  const noEffect = realtimeToolContinuationPolicy([
    { callId: 'noise', name: 'do_nothing', argumentsText: '{"reason":"background noise"}' },
  ]);
  assert.deepEqual(noEffect, { valid: true, shouldRespond: false, instructions: '', failureMessage: '' });

  const routed = realtimeToolContinuationPolicy([
    { callId: 'route', name: 'route_conversation_turn', argumentsText: '{"utterance":"what changed?"}' },
  ]);
  assert.equal(routed.valid, true);
  assert.equal(routed.shouldRespond, true);
  assert.match(routed.instructions, /Speak only the message string/);
  assert.match(routed.instructions, /exactly as written/);
  assert.doesNotMatch(routed.instructions, /answer from model memory/i);

  const duplicateRoute = realtimeToolContinuationPolicy([
    { callId: 'route-1', name: 'route_conversation_turn', argumentsText: '{"utterance":"make a deck"}' },
    { callId: 'route-2', name: 'route_conversation_turn', argumentsText: '{"utterance":"make a deck"}' },
  ]);
  assert.equal(duplicateRoute.valid, false);
  assert.equal(duplicateRoute.shouldRespond, true);
  assert.match(duplicateRoute.failureMessage, /couldn't safely route/i);
  assert.match(duplicateRoute.instructions, /Say exactly/);
  assert.doesNotMatch(duplicateRoute.instructions, /most recent route_conversation_turn/);
});

test('metering reads bounded live audio levels without decorative motion', () => {
  assert.equal(audioLevelFromStats(new Map([
    ['mic', { type: 'media-source', kind: 'audio', audioLevel: 0.42 }],
    ['video', { type: 'media-source', kind: 'video', audioLevel: 1 }],
  ])), 0.42);
  assert.equal(audioLevelFromStats([{ type: 'inbound-rtp', kind: 'audio', audioLevel: 4 }]), 1);
  assert.equal(audioLevelFromStats({}), 0);
});
