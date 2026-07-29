import assert from 'node:assert/strict';
import test from 'node:test';

import { encodeOfficeCommand, parseOfficeEventEnvelope } from '../realtime/officeEventProtocol';

test('office event protocol exposes the nested event payload', () => {
  const raw = JSON.stringify({
    event: 'kanban',
    data: JSON.stringify({ event: 'chat_thread', data: { id: 'thread-1', message: { id: 'message-1' } } }),
  });
  assert.deepEqual(parseOfficeEventEnvelope(raw), {
    event: 'chat_thread',
    data: { id: 'thread-1', message: { id: 'message-1' } },
  });
  assert.equal(parseOfficeEventEnvelope('{broken'), null);
});

test('office commands carry JSON data in the websocket wire shape', () => {
  const encoded = encodeOfficeCommand('chat_typing', { threadId: 'thread-1', typing: true });
  assert.ok(encoded);
  const command = JSON.parse(encoded!) as { event: string; data: string };
  assert.equal(command.event, 'chat_typing');
  assert.deepEqual(JSON.parse(command.data), { threadId: 'thread-1', typing: true });
});
