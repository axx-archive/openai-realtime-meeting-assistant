import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  ROOM_CHAT_HISTORY_LIMIT,
  ROOM_CONVERSATION_UNREAD_LIMIT,
  ROOM_TRANSCRIPT_HISTORY_LIMIT,
  createRoomConversationState,
  parseMemoryTranscriptEntry,
  parseRoomChatDelete,
  parseRoomChatHistory,
  parseRoomChatMessage,
  roomChatMessageIsOwn,
  roomConversationReducer,
} from '../realtime/roomConversation';

const BASE_TIME = '2026-07-25T19:08:52.123456789Z';

function chat(
  id: string,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id,
    name: 'Erick',
    text: `message ${id}`,
    createdAt: BASE_TIME,
    roomId: 'the-office',
    authorEmail: 'erick@shareability.com',
    ...overrides,
  };
}

function transcript(
  id: string,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id,
    kind: 'transcript',
    text: `AJ: transcript ${id}`,
    createdAt: BASE_TIME,
    metadata: {
      roomId: 'the-office',
      speaker: 'AJ',
      source: 'transcript_lane',
    },
    ...overrides,
  };
}

describe('native room conversation', () => {
  it('parses the exact durable room-chat wire shape without inventing identity or time', () => {
    const parsed = parseRoomChatMessage(chat('chat-1', {
      name: '  Guest   Sam\n',
      text: '  first line\r\nsecond line  ',
      artifactId: ' os-artifact-1 ',
      authorEmail: ' ERICK@SHAREABILITY.COM ',
    }));
    assert.deepEqual(parsed, {
      id: 'chat-1',
      name: 'Guest Sam',
      text: 'first line\nsecond line',
      createdAt: BASE_TIME,
      roomId: 'the-office',
      artifactId: 'os-artifact-1',
      authorEmail: 'erick@shareability.com',
    });

    assert.equal(parseRoomChatMessage(chat('', {})), null);
    assert.equal(parseRoomChatMessage(chat('chat-2', { text: { unsafe: true } })), null);
    assert.equal(parseRoomChatMessage(chat('chat-3', { createdAt: 'not-a-date' })), null);
    assert.equal(parseRoomChatMessage(chat('chat-4', { id: 'bad\nid' })), null);
    assert.equal(parseRoomChatMessage('not an object'), null);
    assert.equal(parseRoomChatDelete({ id: ' chat-1 ', roomId: 'the-office' })?.id, 'chat-1');
    assert.equal(parseRoomChatDelete({ id: { unsafe: true } }), null);
  });

  it('parses memory_transcript metadata and removes only its authoritative speaker prefix', () => {
    const parsed = parseMemoryTranscriptEntry(transcript('transcript-1', {
      text: 'AJ: ship the release',
      metadata: {
        roomId: 'the-office',
        speaker: ' AJ ',
        source: 'transcript_lane',
        speakerConfidence: 'dominant',
        unsafe: { nested: true },
      },
    }));
    assert.equal(parsed?.id, 'transcript-1');
    assert.equal(parsed?.speaker, 'AJ');
    assert.equal(parsed?.text, 'ship the release');
    assert.equal(parsed?.rawText, 'AJ: ship the release');
    assert.equal(parsed?.createdAt, BASE_TIME);
    assert.equal(parsed?.roomId, 'the-office');
    assert.equal(parsed?.metadata.speakerConfidence, 'dominant');
    assert.equal(Object.hasOwn(parsed?.metadata ?? {}, 'unsafe'), false);

    const colonInSpeech = parseMemoryTranscriptEntry(transcript('transcript-2', {
      text: 'Agenda: ship the release',
    }));
    assert.equal(colonInSpeech?.text, 'Agenda: ship the release');
    assert.equal(parseMemoryTranscriptEntry({ ...transcript('wrong-kind'), kind: 'brain' }), null);
    assert.equal(parseMemoryTranscriptEntry({ ...transcript('no-time'), createdAt: null }), null);
  });

  it('treats a valid room_chat_history array as authoritative, deduped, room-scoped, and bounded', () => {
    let state = createRoomConversationState('the-office');
    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('old'),
      chatOpen: false,
    });
    assert.equal(state.unreadCount, 1);

    const history = Array.from({ length: ROOM_CHAT_HISTORY_LIMIT + 5 }, (_, index) => chat(`chat-${index}`));
    history.splice(2, 0, chat('chat-1', { text: 'duplicate must not replace the first' }));
    history.push(chat('foreign', { roomId: 'another-room' }));
    state = roomConversationReducer(state, { type: 'room_chat_history', payload: history });

    assert.equal(state.messages.length, ROOM_CHAT_HISTORY_LIMIT);
    assert.equal(state.messages[0]?.id, 'chat-5');
    assert.equal(state.messages.at(-1)?.id, `chat-${ROOM_CHAT_HISTORY_LIMIT + 4}`);
    assert.equal(state.unreadCount, 1, 'history replay is not a new-message notification');
    assert.equal(state.historyEstablished, true);

    const beforeMalformed = state;
    state = roomConversationReducer(state, { type: 'room_chat_history', payload: { messages: [] } });
    assert.equal(state, beforeMalformed, 'a malformed snapshot must not erase known-good state');
    assert.equal(parseRoomChatHistory('malformed'), null);

    state = roomConversationReducer(state, { type: 'room_chat_history', payload: [] });
    assert.deepEqual(state.messages, [], 'an explicit empty array is authoritative');
  });

  it('uses the initial room history as a read baseline, then counts only new reconnect messages', () => {
    const viewer = { email: 'tom@shareability.com', name: 'Tom' };
    let state = createRoomConversationState('the-office');
    state = roomConversationReducer(state, {
      type: 'room_chat_history',
      payload: [chat('baseline-1'), chat('baseline-2')],
      chatOpen: false,
      viewer,
    });
    assert.equal(state.unreadCount, 0, 'initial join history is a baseline, not an unread burst');
    assert.equal(state.historyEstablished, true);

    state = roomConversationReducer(state, {
      type: 'room_chat_history',
      payload: [
        chat('baseline-1'),
        chat('baseline-2'),
        chat('reconnect-other'),
        chat('reconnect-other', { text: 'duplicate replay' }),
        chat('reconnect-own', { name: 'Renamed Tom', authorEmail: 'TOM@shareability.com' }),
      ],
      chatOpen: false,
      viewer,
    });
    assert.deepEqual(
      state.messages.map((message) => message.id),
      ['baseline-1', 'baseline-2', 'reconnect-other', 'reconnect-own'],
    );
    assert.equal(state.unreadCount, 1, 'only the truly new other-user message is unread');

    const afterDuplicateReplay = roomConversationReducer(state, {
      type: 'room_chat_history',
      payload: [...state.messages],
      chatOpen: false,
      viewer,
    });
    assert.equal(afterDuplicateReplay.unreadCount, 1, 'an identical reconnect replay cannot increment twice');
  });

  it('does not count reconnect history while chat is open and honors the explicit read boundary', () => {
    const viewer = { email: 'tom@shareability.com', name: 'Tom' };
    let state = roomConversationReducer(createRoomConversationState('the-office'), {
      type: 'room_chat_history',
      payload: [chat('baseline')],
      chatOpen: false,
      viewer,
    });
    state = roomConversationReducer(state, {
      type: 'room_chat_history',
      payload: [chat('baseline'), chat('visible-while-open')],
      chatOpen: true,
      viewer,
    });
    assert.equal(state.unreadCount, 0);

    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('closed-live'),
      chatOpen: false,
      viewer,
    });
    assert.equal(state.unreadCount, 1);
    state = roomConversationReducer(state, { type: 'mark_read' });
    assert.equal(state.unreadCount, 0);

    state = roomConversationReducer(state, {
      type: 'room_chat_history',
      payload: [chat('baseline'), chat('visible-while-open'), chat('closed-live'), chat('next-open')],
      chatOpen: true,
      viewer,
    });
    assert.equal(state.unreadCount, 0, 'open chat remains read across reconnect history reconciliation');
  });

  it('counts only new, unread messages from another account and caps the badge', () => {
    const viewer = { email: 'tom@shareability.com', name: 'Tom' };
    let state = createRoomConversationState('the-office');

    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('own-email', { name: 'Renamed Tom', authorEmail: 'TOM@shareability.com' }),
      chatOpen: false,
      viewer,
    });
    assert.equal(state.unreadCount, 0);
    assert.equal(roomChatMessageIsOwn(state.messages[0]!, viewer), true);

    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('email-wins', { name: 'Tom', authorEmail: 'someone-else@shareability.com' }),
      chatOpen: false,
      viewer,
    });
    assert.equal(state.unreadCount, 1, 'a stamped non-matching email must win over the mutable name');

    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('legacy-own', { name: ' tom ', authorEmail: undefined }),
      chatOpen: false,
      viewer,
    });
    assert.equal(state.unreadCount, 1, 'legacy unstamped messages fall back to the name');

    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('open-chat'),
      chatOpen: true,
      viewer,
    });
    assert.equal(state.unreadCount, 1);

    const beforeDuplicate = state;
    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('open-chat'),
      chatOpen: false,
      viewer,
    });
    assert.equal(state, beforeDuplicate, 'a duplicate cannot increment unread');

    for (let index = 0; index < ROOM_CONVERSATION_UNREAD_LIMIT + 10; index += 1) {
      state = roomConversationReducer(state, {
        type: 'room_chat',
        payload: chat(`unread-${index}`),
        chatOpen: false,
        viewer,
      });
    }
    assert.equal(state.unreadCount, ROOM_CONVERSATION_UNREAD_LIMIT);
  });

  it('applies room_chat_delete to the bubble and matching typed transcript without guessing unread state', () => {
    let state = createRoomConversationState('the-office');
    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('chat-delete-me'),
      chatOpen: false,
    });
    state = roomConversationReducer(state, {
      type: 'memory_transcript',
      payload: transcript('chat-delete-me', {
        text: 'Erick: remove this',
        metadata: { roomId: 'the-office', speaker: 'Erick', source: 'room_chat' },
      }),
    });
    state = roomConversationReducer(state, {
      type: 'memory_transcript',
      payload: transcript('spoken-keep-me'),
    });

    state = roomConversationReducer(state, {
      type: 'room_chat_delete',
      payload: { id: 'chat-delete-me', roomId: 'the-office' },
    });
    assert.deepEqual(state.messages, []);
    assert.deepEqual(state.transcriptEntries.map((entry) => entry.id), ['spoken-keep-me']);
    assert.equal(state.unreadCount, 1);

    const beforeForeignDelete = state;
    state = roomConversationReducer(state, {
      type: 'room_chat_delete',
      payload: { id: 'spoken-keep-me', roomId: 'another-room' },
    });
    assert.equal(state, beforeForeignDelete);
  });

  it('dedupes and bounds live transcript entries while preserving server ids and timestamps', () => {
    let state = createRoomConversationState('the-office');
    for (let index = 0; index < ROOM_TRANSCRIPT_HISTORY_LIMIT + 5; index += 1) {
      state = roomConversationReducer(state, {
        type: 'memory_transcript',
        payload: transcript(`transcript-${index}`),
      });
    }
    assert.equal(state.transcriptEntries.length, ROOM_TRANSCRIPT_HISTORY_LIMIT);
    assert.equal(state.transcriptEntries[0]?.id, 'transcript-5');
    assert.equal(state.transcriptEntries.at(-1)?.createdAt, BASE_TIME);

    const beforeDuplicate = state;
    state = roomConversationReducer(state, {
      type: 'memory_transcript',
      payload: transcript('transcript-204', { text: 'AJ: replacement' }),
    });
    assert.equal(state, beforeDuplicate);

    state = roomConversationReducer(state, {
      type: 'memory_transcript',
      payload: transcript('foreign', {
        metadata: { roomId: 'another-room', speaker: 'AJ', source: 'transcript_lane' },
      }),
    });
    assert.equal(state, beforeDuplicate);
  });

  it('marks the chat read and resets all room-scoped conversation state', () => {
    let state = createRoomConversationState('the-office');
    state = roomConversationReducer(state, {
      type: 'room_chat',
      payload: chat('chat-1'),
      chatOpen: false,
    });
    state = roomConversationReducer(state, { type: 'mark_read' });
    assert.equal(state.unreadCount, 0);
    assert.equal(roomConversationReducer(state, { type: 'mark_read' }), state);

    state = roomConversationReducer(state, { type: 'reset', roomId: 'next-room' });
    assert.deepEqual(state, {
      roomId: 'next-room',
      messages: [],
      transcriptEntries: [],
      unreadCount: 0,
      historyEstablished: false,
    });
  });
});
