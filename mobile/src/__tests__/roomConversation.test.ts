import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  ROOM_CHAT_HISTORY_LIMIT,
  ROOM_CONVERSATION_UNREAD_LIMIT,
  ROOM_TRANSCRIPT_HISTORY_LIMIT,
  createRoomConversationState,
  latestRoomConversationActivity,
  parseMeetingTranscriptSnapshot,
  parseMemoryTranscriptEntry,
  parseRoomChatDelete,
  parseRoomChatHistory,
  parseRoomChatMessage,
  roomChatMessageIsOwn,
  roomChatMessageBelongsInConversation,
  roomConversationActivityMessages,
  roomConversationFeedMessages,
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
      captureSequence: String(Number(id.match(/\d+$/u)?.[0] ?? '99')),
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

  it('replays one exact current-sitting transcript snapshot and rejects mixed or duplicate records', () => {
    const payload = {
      contract: 'meeting-transcript-v1',
      roomId: 'the-office',
      meetingId: 'meeting-current',
      entries: [
        transcript('transcript-1', { metadata: { roomId: 'the-office', meetingId: 'meeting-current', speaker: 'AJ', source: 'transcript_lane', captureSequence: '1' } }),
        transcript('transcript-2', { metadata: { roomId: 'the-office', meetingId: 'meeting-current', speaker: 'Tim', source: 'transcript_lane', captureSequence: '2' } }),
      ],
    };
    assert.equal(parseMeetingTranscriptSnapshot(payload)?.entries.length, 2);
    let state = roomConversationReducer(createRoomConversationState('the-office'), {
      type: 'meeting_transcript_snapshot', payload,
    });
    assert.deepEqual(state.transcriptEntries.map((entry) => entry.id), ['transcript-1', 'transcript-2']);

    const retained = state;
    state = roomConversationReducer(state, {
      type: 'meeting_transcript_snapshot',
      payload: { ...payload, entries: [...payload.entries, transcript('foreign', { metadata: { roomId: 'another-room', meetingId: 'meeting-current', captureSequence: '3' } })] },
    });
    assert.equal(state, retained);
    assert.equal(parseMeetingTranscriptSnapshot({ ...payload, entries: [payload.entries[0], payload.entries[0]] }), null);
    assert.equal(parseMeetingTranscriptSnapshot({ ...payload, entries: [
      { ...payload.entries[0], metadata: { roomId: 'the-office', meetingId: 'meeting-current', speaker: 'AJ', source: 'transcript_lane' } },
    ] }), null);
    assert.equal(parseMeetingTranscriptSnapshot({ ...payload, entries: [payload.entries[1], payload.entries[0]] }), null);

    state = roomConversationReducer(state, {
      type: 'memory_transcript',
      payload: transcript('transcript-3', { metadata: { roomId: 'the-office', meetingId: 'meeting-current', speaker: 'AJ', source: 'transcript_lane', captureSequence: '3' } }),
    });
    state = roomConversationReducer(state, { type: 'meeting_transcript_snapshot', payload });
    assert.deepEqual(state.transcriptEntries.map((entry) => entry.id), ['transcript-1', 'transcript-2', 'transcript-3']);
  });

  it('preserves server-owned durable follow-through receipts for visible work state', () => {
    const parsed = parseRoomChatMessage(chat('chat-follow-through', {
      agentId: 'scout',
      followThroughId: 'room-follow-through-123',
      followThroughStatus: 'queued',
      destinationThreadId: 'thread-ball-dogs',
    }));
    assert.equal(parsed?.followThroughId, 'room-follow-through-123');
    assert.equal(parsed?.followThroughStatus, 'queued');
    assert.equal(parsed?.destinationThreadId, 'thread-ball-dogs');
    assert.equal(parseRoomChatMessage(chat('chat-invalid-follow-through', {
      followThroughId: 'room-follow-through-123',
      followThroughStatus: 'invented',
    }))?.followThroughId, undefined);
  });

	it('renders one evolving room work identity across running, restart replay, and needs-attention', () => {
		const running = chat('room-work-running', {
			name: 'Scout', agentId: 'scout', artifactId: 'os-artifact-research-1',
			workRunId: 'agent-thread-research-1', workStatus: 'running', workFamily: 'Research',
			workTitle: 'Partner landscape', workProgress: '35',
		});
		const blocked = chat('room-work-needs-attention', {
			name: 'Scout', agentId: 'scout', artifactId: 'os-artifact-research-1',
			workRunId: 'agent-thread-research-1', workStatus: 'needs_attention', workFamily: 'Research',
			workTitle: 'Partner landscape', workProgress: '72',
		});
		const parsed = parseRoomChatMessage(running);
		assert.equal(parsed?.workRunId, 'agent-thread-research-1');
		assert.equal(parsed?.workStatus, 'running');
		assert.equal(parsed?.workProgress, 35);
		assert.deepEqual(parseRoomChatHistory([running, blocked])?.map((message) => message.id), ['room-work-needs-attention']);

		let state = createRoomConversationState('the-office');
		state = roomConversationReducer(state, { type: 'room_chat', payload: running, chatOpen: true });
		state = roomConversationReducer(state, { type: 'room_chat', payload: blocked, chatOpen: true });
		assert.equal(state.messages.length, 1);
		assert.equal(state.messages[0]?.id, 'room-work-needs-attention');
		assert.equal(state.messages[0]?.workStatus, 'needs_attention');
	});

  it('keeps room lifecycle in Activity and admits only an exact typed final deliverable to chat', () => {
    const digest = 'd'.repeat(64);
    const running = parseRoomChatMessage(chat('work-running', {
      name: 'Scout', agentId: 'scout', artifactId: 'work-root-artifact',
      workRunId: 'work-root', workRootRunId: 'work-root', workStatus: 'running',
      workFamily: 'Visual', workTitle: 'Campaign image', workProgress: 42,
    }))!;
    const needsInput = parseRoomChatMessage(chat('work-input', {
      name: 'Scout', agentId: 'scout', artifactId: 'work-root-artifact',
      workRunId: 'work-root', workRootRunId: 'work-root', workStatus: 'needs_input',
      workFamily: 'Visual', workTitle: 'Campaign image', workProgress: 68,
    }))!;
    const complete = parseRoomChatMessage(chat('work-complete', {
      name: 'Scout', agentId: 'scout', artifactId: 'work-root-artifact',
      workRunId: 'work-root', workRootRunId: 'work-root', workStatus: 'complete',
      workFamily: 'Visual', workTitle: 'Campaign image', workProgress: 100,
      resultArtifactId: 'campaign-image-result', resultArtifactType: 'image',
      resultArtifactVersion: 3, resultArtifactDigest: digest, resultTitle: 'Campaign hero',
    }))!;
    const malformedComplete = parseRoomChatMessage(chat('work-malformed-complete', {
      name: 'Scout', agentId: 'scout', artifactId: 'malformed-root',
      workRunId: 'malformed', workRootRunId: 'malformed', workStatus: 'complete',
      resultArtifactId: 'unsafe-result', resultArtifactType: 'image',
      resultArtifactVersion: 4, resultArtifactDigest: '{"raw":"json"}',
    }))!;
    const ordinary = parseRoomChatMessage(chat('human-decision', { text: 'Use the second direction.' }))!;
    const followThrough = parseRoomChatMessage(chat('follow-through', {
      agentId: 'scout', followThroughId: 'follow-1', followThroughStatus: 'awaiting_input',
    }))!;

    assert.equal(complete.workRootRunId, 'work-root');
    assert.equal(complete.resultArtifactVersion, 3);
    assert.equal(complete.resultArtifactDigest, digest);
    assert.equal(roomChatMessageBelongsInConversation(running), false);
    assert.equal(roomChatMessageBelongsInConversation(needsInput), false);
    assert.equal(roomChatMessageBelongsInConversation(malformedComplete), false);
    assert.equal(roomChatMessageBelongsInConversation(complete), true);
    assert.deepEqual(roomConversationFeedMessages([ordinary, running, needsInput, complete, malformedComplete, followThrough]).map(({ id }) => id), ['human-decision', 'work-complete']);
    assert.deepEqual(roomConversationActivityMessages([ordinary, running, needsInput, complete, malformedComplete, followThrough]).map(({ id }) => id), ['work-complete', 'work-malformed-complete', 'follow-through']);
  });

  it('reorders a revised root to the newest Activity position after delegated work', () => {
    const rootRunning = chat('root-running', {
      agentId: 'scout', workRunId: 'root-run', workRootRunId: 'root-run', workStatus: 'running', workTitle: 'Build presentation',
    });
    const childRunning = chat('child-running', {
      agentId: 'designer', workRunId: 'child-run', workRootRunId: 'root-run', workParentRunId: 'root-run', workStatus: 'running', workTitle: 'Design pass',
    });
    const rootComplete = chat('root-complete', {
      agentId: 'scout', workRunId: 'root-run', workRootRunId: 'root-run', workStatus: 'complete', workTitle: 'Build presentation',
      resultArtifactId: 'deck-result', resultArtifactType: 'html_deck', resultArtifactVersion: 3, resultArtifactDigest: 'f'.repeat(64),
    });
    let state = createRoomConversationState('the-office');
    state = roomConversationReducer(state, { type: 'room_chat', payload: rootRunning, chatOpen: true });
    state = roomConversationReducer(state, { type: 'room_chat', payload: childRunning, chatOpen: true });
    state = roomConversationReducer(state, { type: 'room_chat', payload: rootComplete, chatOpen: true });

    assert.deepEqual(state.messages.map(({ id }) => id), ['child-running', 'root-complete']);
    assert.equal(latestRoomConversationActivity(state.messages)?.id, 'root-complete');
    assert.deepEqual(roomConversationActivityMessages(state.messages).map(({ id }) => id), ['child-running', 'root-complete']);
  });

  it('rejects malformed result receipts and delegated results without explicit root topology', () => {
    const exact = parseRoomChatMessage(chat('exact-result', {
      workRunId: 'root-run',
      workStatus: 'complete',
      resultArtifactId: 'deck-1',
      resultArtifactType: 'html_deck',
      resultArtifactVersion: 7,
      resultArtifactDigest: 'a'.repeat(64),
    }))!;
    assert.deepEqual(roomConversationFeedMessages([exact]).map(({ id }) => id), ['exact-result']);
    assert.deepEqual(roomConversationFeedMessages([{ ...exact, id: 'zero-version', resultArtifactVersion: 0 }]), []);
    assert.deepEqual(roomConversationFeedMessages([{ ...exact, id: 'bad-digest', resultArtifactDigest: 'a' }]), []);
    assert.deepEqual(roomConversationFeedMessages([{
      ...exact,
      id: 'orphan-child',
      workRunId: 'child-run',
      workParentRunId: 'root-run',
      workRootRunId: undefined,
    }]), []);
  });

  it('does not turn lifecycle into unread chat and dedupes result supersession by explicit root and artifact identity', () => {
    const viewer = { email: 'tom@shareability.com', name: 'Tom' };
    const digestV1 = '1'.repeat(64);
    const digestV2 = '2'.repeat(64);
    const lifecycle = chat('delegated-running', {
      name: 'Scout', agentId: 'scout', artifactId: 'child-owner',
      workRunId: 'child-run', workRootRunId: 'root-run', workParentRunId: 'root-run',
      workStatus: 'running', workTitle: 'Data pass',
    });
    const v1 = parseRoomChatMessage(chat('result-v1', {
      name: 'Scout', agentId: 'scout', artifactId: 'root-owner', workRunId: 'root-run', workRootRunId: 'root-run', workStatus: 'complete',
      resultArtifactId: 'table-result', resultArtifactType: 'table', resultArtifactVersion: 1, resultArtifactDigest: digestV1,
    }))!;
    const v2 = parseRoomChatMessage(chat('result-v2', {
      name: 'Scout', agentId: 'scout', artifactId: 'root-owner', workRunId: 'revision-run', workRootRunId: 'root-run', workParentRunId: 'root-run', workStatus: 'complete',
      resultArtifactId: 'table-result', resultArtifactType: 'table', resultArtifactVersion: 2, resultArtifactDigest: digestV2,
    }))!;

    let state = roomConversationReducer(createRoomConversationState('the-office'), {
      type: 'room_chat', payload: lifecycle, chatOpen: false, viewer,
    });
    assert.equal(state.unreadCount, 0, 'generic progress never increments the chat badge');
    state = roomConversationReducer(state, {
      type: 'room_chat', payload: { ...v2 }, chatOpen: false, viewer,
    });
    assert.equal(state.unreadCount, 1, 'the exact typed result is a real conversation arrival');
    assert.deepEqual(roomConversationFeedMessages([v1, v2]).map(({ id }) => id), ['result-v2']);
    assert.equal(v2.workParentRunId, 'root-run');
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
