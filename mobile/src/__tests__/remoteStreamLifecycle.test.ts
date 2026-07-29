import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { createRemoteStreamRetirementQueue } from '../realtime/remoteStreamLifecycle';

function fixture() {
  const releases: boolean[] = [];
  const stream = {
    release: (releaseTracks = true) => { releases.push(releaseTracks); },
  };
  const track = {
    onmute: () => undefined,
    onunmute: () => undefined,
    onended: () => undefined,
  };
  return { entry: { stream, track }, stream, track, releases };
}

describe('remote native stream retirement', () => {
  it('waits until the RTCView feed is no longer committed', () => {
    const queue = createRemoteStreamRetirementQueue();
    const { entry, stream, track, releases } = fixture();

    assert.equal(queue.retire(entry), true);
    assert.equal(track.onmute, null);
    assert.equal(track.onunmute, null);
    assert.equal(track.onended, null);
    assert.equal(queue.flush(new Set([stream])), 0);
    assert.deepEqual(releases, []);

    assert.equal(queue.flush(new Set()), 1);
    assert.deepEqual(releases, [false]);
  });

  it('releases each wrapper exactly once across overlapping cleanup paths', () => {
    const queue = createRemoteStreamRetirementQueue();
    const { entry, releases } = fixture();

    assert.equal(queue.retire(entry), true);
    assert.equal(queue.retire(entry), false);
    assert.equal(queue.flushAll(), 1);
    assert.equal(queue.retire(entry), false);
    assert.equal(queue.flushAll(), 0);
    assert.deepEqual(releases, [false]);
  });
});
