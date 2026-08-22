import assert from 'node:assert/strict';
import test from 'node:test';
import {
  FOCUSED_ACTIVE_LANE_HEIGHT,
  FOCUSED_ERROR_LANE_HEIGHT,
  personalRealtimeIslandPlacement,
  personalRealtimeIslandSurface,
} from '../realtime/personalRealtimeIslandPlacement';

const base = {
  bottomInset: 34,
  expanded: false,
  rightInset: 0,
  smallSpace: 8,
  largeSpace: 16,
  topInset: 47,
};

test('routes select explicit voice lanes instead of guessing at bottom controls', () => {
  assert.equal(personalRealtimeIslandSurface('Thread'), 'conversation');
  assert.equal(personalRealtimeIslandSurface('ChannelRiff'), 'conversation');
  assert.equal(personalRealtimeIslandSurface('Room'), 'room');
  assert.equal(personalRealtimeIslandSurface('DeckViewer'), 'focused-workspace');
  assert.equal(personalRealtimeIslandSurface('Canvas'), 'shell');
});

test('focused iPhone voice reserves a top lane outside Thread header and composer geometry', () => {
  assert.deepEqual(personalRealtimeIslandPlacement({
    ...base,
    layout: 'compact',
    surface: 'conversation',
  }), {
    contentTopInset: FOCUSED_ACTIVE_LANE_HEIGHT,
    docked: true,
    right: undefined,
    top: 47,
  });
});

test('a three-line accessible error expands the reserved lane without moving over controls', () => {
  assert.deepEqual(personalRealtimeIslandPlacement({
    ...base,
    expanded: true,
    layout: 'compact',
    surface: 'conversation',
  }), {
    contentTopInset: FOCUSED_ERROR_LANE_HEIGHT,
    docked: true,
    right: undefined,
    top: 47,
  });
});

test('focused iPad voice preserves its rail and owns a top-right lane', () => {
  assert.deepEqual(personalRealtimeIslandPlacement({
    ...base,
    layout: 'sidebar',
    surface: 'room',
  }), {
    contentTopInset: FOCUSED_ACTIVE_LANE_HEIGHT,
    docked: false,
    right: 16,
    top: 47,
  });
});

test('ordinary shell routes retain their established nav-relative placement', () => {
  assert.deepEqual(personalRealtimeIslandPlacement({
    ...base,
    layout: 'compact',
    surface: 'shell',
  }), {
    bottom: 104,
    contentTopInset: 0,
    docked: true,
  });
  assert.deepEqual(personalRealtimeIslandPlacement({
    ...base,
    layout: 'sidebar',
    surface: 'shell',
  }), {
    bottom: 34,
    contentTopInset: 0,
    docked: false,
    right: 16,
  });
});
