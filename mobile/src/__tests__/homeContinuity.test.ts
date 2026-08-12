import assert from 'node:assert/strict';
import test from 'node:test';
import type { Room, ScoutThread } from '../api/types';
import { buildHomeContinuity } from '../canvas/homeContinuity';
import { workFamilyLabel } from '../messaging/workPresentation';

const liveRoom: Room = {
  id: 'office', name: 'Office', live: true, participantCount: 7,
  passcodeRequired: false, guestEnabled: false, guestLinkActive: false,
  createdBy: 'aj@example.com', archived: false,
};

test('Home prioritizes intervention, live presence, then active work without dashboard tiles', () => {
  const threads: ScoutThread[] = [{
    id: 'thread-work', title: 'Investor package', updatedAt: '2026-08-11T18:57:00Z',
    messages: [{
      id: 'message-work', role: 'assistant', createdAt: '2026-08-11T18:56:00Z',
      thread: {
        id: 'run-1', mode: 'pitch deck', query: 'Build the investor pitch deck',
        status: 'running', currentStage: 'assemble_deck', progressNote: 'Building the first draft',
      },
    }],
  }];
  const result = buildHomeContinuity({
    viewerEmail: 'aj@example.com',
    notifications: [{
      id: 'notice-1', userEmail: 'aj@example.com', text: 'The deck is ready for your approval',
      createdAt: '2026-08-11T18:58:00Z',
    }],
    rooms: [liveRoom], threads,
  });
  assert.deepEqual(result.map((item) => item.kind), ['needs-you', 'live-meeting', 'active-work']);
  assert.equal(result[2]?.eyebrow, 'Presentation · Building');
  assert.equal(result[2]?.detail, 'Building the first draft');
});

test('a recurring pitch deck stays Presentation while genuinely mixed work stays Mixed package', () => {
  const result = buildHomeContinuity({
    viewerEmail: 'aj@example.com', notifications: [], rooms: [], threads: [{
      id: 'deck-thread', title: 'Deck', updatedAt: '2026-08-11T18:57:00Z',
      messages: [{
        id: 'deck-run', role: 'assistant', createdAt: '2026-08-11T18:56:00Z',
        thread: { id: 'deck-run', mode: 'pitch deck', query: 'Create a 10-slide pitch deck', status: 'running' },
      }],
    }],
  });
  assert.match(result[0]?.eyebrow ?? '', /^Presentation ·/u);
  assert.equal(workFamilyLabel({
    mode: 'investor package',
    query: 'Build the research memo, pitch deck, and financial model',
  }), 'Mixed package');
});

test('Home collapses active work and recent continuity for the same thread', () => {
  const threads: ScoutThread[] = [{
    id: 'thread-work', title: 'Model revision', updatedAt: '2026-08-11T18:57:00Z', preview: 'Latest note',
    messages: [{
      id: 'message-work', role: 'assistant', createdAt: '2026-08-11T18:56:00Z',
      thread: { id: 'run-1', mode: 'financial model', query: 'Revise the forecast', status: 'needs_attention' },
    }],
  }];
  const result = buildHomeContinuity({ viewerEmail: 'aj@example.com', notifications: [], rooms: [], threads });
  assert.equal(result.length, 1);
  assert.equal(result[0]?.kind, 'active-work');
  assert.match(result[0]?.eyebrow ?? '', /Needs attention/);
});

test('Home uses human copy for an ordinary recent thread', () => {
  const result = buildHomeContinuity({
    viewerEmail: 'aj@example.com', notifications: [], rooms: [],
    threads: [{
      id: 'thread-private', title: 'Launch plan', updatedAt: '2026-08-11T18:57:00Z',
      lastMessage: { text: 'Next, compare the two launch sequences.', createdAt: '2026-08-11T18:57:00Z' },
    }],
  });
  assert.equal(result[0]?.kind, 'recent-thread');
  assert.equal(result[0]?.detail, 'Next, compare the two launch sequences.');
  assert.deepEqual(result[0]?.destination, { route: 'Thread', threadId: 'thread-private', title: 'Launch plan' });
});
