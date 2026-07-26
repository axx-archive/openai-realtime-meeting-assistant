import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  endpointForTrack,
  indexParticipantTrackEndpoint,
  reconcileRemoteParticipantEndpoints,
  removeRemoteTrackIdentity,
  retainRemoteTrackIndexForFeeds,
} from '../realtime/trackIdentity';

describe('native participant endpoint identity', () => {
  it('binds endpoint identity to forwarded and source ids across metadata ordering', () => {
    const index = indexParticipantTrackEndpoint(new Map(), {
      endpointId: 'aj-desktop',
      name: 'AJ',
      sourceTrackId: 'desktop-source',
      trackId: 'stream:desktop-source:101',
    });

    assert.equal(endpointForTrack('stream:desktop-source:101', index), 'aj-desktop');
    assert.equal(endpointForTrack('desktop-source', index), 'aj-desktop');
  });

  it('removes only the departed endpoint track identity', () => {
    let index = indexParticipantTrackEndpoint(new Map(), {
      endpointId: 'aj-desktop',
      sourceTrackId: 'desktop-source',
      trackId: 'stream:desktop-source:101',
    });
    index = indexParticipantTrackEndpoint(index, {
      endpointId: 'aj-phone',
      sourceTrackId: 'phone-source',
      trackId: 'stream:phone-source:202',
    });

    const retained = retainRemoteTrackIndexForFeeds(index, [
      { trackId: 'stream:desktop-source:101' },
    ]);
    assert.equal(endpointForTrack('desktop-source', retained), 'aj-desktop');
    assert.equal(endpointForTrack('phone-source', retained), undefined);

    const empty = removeRemoteTrackIdentity(retained, 'stream:desktop-source:101');
    assert.equal(empty.size, 0);
  });

  it('retires a departed device immediately while the same participant stays present', () => {
    const participantsByTrack = new Map([
      ['desktop-source', 'AJ'],
      ['stream:desktop-source:101', 'AJ'],
      ['phone-source', 'AJ'],
      ['stream:phone-source:202', 'AJ'],
    ]);
    const endpointsByTrack = new Map([
      ['desktop-source', 'aj-desktop'],
      ['stream:desktop-source:101', 'aj-desktop'],
      ['phone-source', 'aj-phone'],
      ['stream:phone-source:202', 'aj-phone'],
    ]);

    const reconciled = reconcileRemoteParticipantEndpoints(
      [
        { trackId: 'stream:desktop-source:101' },
        { trackId: 'stream:phone-source:202' },
      ],
      participantsByTrack,
      endpointsByTrack,
      { aj: { 'aj-desktop': {} } },
    );

    assert.deepEqual(reconciled.feeds.map((feed) => feed.trackId), ['stream:desktop-source:101']);
    assert.equal(reconciled.participantsByTrack.has('phone-source'), false);
    assert.equal(reconciled.endpointsByTrack.has('phone-source'), false);
    assert.equal(endpointForTrack('desktop-source', reconciled.endpointsByTrack), 'aj-desktop');
  });

  it('preserves unlabeled feeds and all media when no authoritative endpoint snapshot exists', () => {
    const participantsByTrack = new Map([['known-source', 'AJ']]);
    const endpointsByTrack = new Map([['known-source', 'aj-phone']]);
    const feeds = [
      { trackId: 'unlabeled-source' },
      { trackId: 'known-source', participant: 'AJ', endpointId: 'aj-phone' },
    ];

    const withoutSnapshot = reconcileRemoteParticipantEndpoints(
      feeds,
      participantsByTrack,
      endpointsByTrack,
      null,
    );
    assert.deepEqual(withoutSnapshot.feeds, feeds);

    const unlabeled = reconcileRemoteParticipantEndpoints(
      [{ trackId: 'unlabeled-source' }],
      new Map(),
      new Map(),
      {},
    );
    assert.equal(unlabeled.feeds.length, 1);
  });
});
