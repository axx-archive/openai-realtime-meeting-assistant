import assert from 'node:assert/strict';
import test from 'node:test';

import {
  bonfireStatusLanePlacement,
  screenRectanglesOverlap,
  type ScreenRectangle,
} from '../canvas/bonfireShortcutPlacement';
import {
  FOCUSED_ERROR_LANE_HEIGHT,
  personalRealtimeIslandPlacement,
} from '../realtime/personalRealtimeIslandPlacement';
import type { NativeShellLayout } from '../navigation/nativeShellModel';

function centeredRealtimeErrorRectangle(input: {
  bottomInset: number;
  height: number;
  layout: NativeShellLayout;
  width: number;
}): ScreenRectangle {
  const placement = personalRealtimeIslandPlacement({
    bottomInset: input.bottomInset,
    expanded: true,
    rightInset: 0,
    layout: input.layout,
    smallSpace: 8,
    largeSpace: 16,
    surface: 'shell',
    topInset: 0,
  });
  const width = Math.min(300, input.width);
  const bottom = placement.bottom ?? 0;
  return {
    height: FOCUSED_ERROR_LANE_HEIGHT,
    left: (input.width - width) / 2,
    top: input.height - bottom - FOCUSED_ERROR_LANE_HEIGHT,
    width,
  };
}

test('Bonfire error lane cannot overlap the centered Realtime island on iPhone or iPad', () => {
  const devices = [
    { name: 'iPhone SE portrait', width: 320, height: 568, bottomInset: 0, layout: 'compact' as const },
    { name: 'iPhone landscape', width: 568, height: 320, bottomInset: 21, layout: 'compact' as const },
    { name: 'iPhone Pro portrait', width: 390, height: 844, bottomInset: 34, layout: 'compact' as const },
    { name: 'iPad Split View', width: 600, height: 1024, bottomInset: 20, layout: 'compact' as const },
    { name: 'iPad landscape', width: 1024, height: 768, bottomInset: 20, layout: 'sidebar' as const },
  ];

  for (const device of devices) {
    const status = bonfireStatusLanePlacement(device.width, device.height);
    const voice = centeredRealtimeErrorRectangle(device);
    assert.equal(screenRectanglesOverlap(status, voice), false, device.name);
  }
});

test('short landscape phones use the compact truthful notice', () => {
  assert.deepEqual(bonfireStatusLanePlacement(568, 320), {
    compact: true,
    height: 56,
    left: 20,
    top: 52,
    width: 96,
  });
  assert.equal(bonfireStatusLanePlacement(390, 844).compact, false);
});
