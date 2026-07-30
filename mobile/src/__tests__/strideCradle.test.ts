import assert from 'node:assert/strict';
import test from 'node:test';
import {
  STRIDE_CRADLE_TRANSFER_SECONDS,
  createStrideCradleState,
  stepStrideCradle,
  strideCradleContactWeights,
  strideCradleTransferProgress,
  type StrideCradleState,
} from '../theme/strideCradle';

function advanceUntil(
  predicate: (state: StrideCradleState) => boolean,
  level = 0.8,
  limitSeconds = 2,
) {
  const state = createStrideCradleState(level);
  for (let elapsed = 0; elapsed < limitSeconds; elapsed += 1 / 240) {
    stepStrideCradle(state, 1 / 240, level);
    if (predicate(state)) return state;
  }
  assert.fail('cradle state did not reach the expected event');
}

test('the released left mass alone swings toward the fixed row', () => {
  const state = createStrideCradleState(0.8);
  const releasedAt = state.leftAngle;
  stepStrideCradle(state, 0.1, 0.8);
  assert.ok(state.leftAngle > releasedAt);
  assert.equal(state.rightAngle, 0);
  assert.equal(state.rightVelocity, 0);
});

test('agent audio releases the right mass and transfers toward the human', () => {
  const state = createStrideCradleState(0.8, 'agent');
  const releasedAt = state.rightAngle;
  assert.equal(state.leftAngle, 0);
  stepStrideCradle(state, 0.1, 0.8, 'agent');
  assert.ok(state.rightAngle < releasedAt);

  let returnedToHuman = false;
  for (let elapsed = 0; elapsed < 1; elapsed += 1 / 240) {
    stepStrideCradle(state, 1 / 240, 0.8, 'agent');
    if (state.transferDirection === -1 && state.transferAge < 0.01) {
      returnedToHuman = true;
      break;
    }
  }
  assert.equal(returnedToHuman, true);
  assert.ok(state.leftVelocity < 0);
  assert.equal(state.rightVelocity, 0);
});

test('an equal-mass impact stops the left and launches only the far right', () => {
  const state = advanceUntil(function (candidate) {
    return candidate.transferDirection === 1 && candidate.transferAge < 0.01;
  });
  assert.equal(state.leftAngle, 0);
  assert.equal(state.leftVelocity, 0);
  assert.ok(state.rightVelocity > 0);
  assert.ok(strideCradleTransferProgress(state) !== null);
});

test('the sub-frame collision leaves at least four frames of perceptual trace', () => {
  const state = advanceUntil(function (candidate) {
    return candidate.transferDirection === 1 && candidate.transferAge < 0.01;
  });
  assert.equal(STRIDE_CRADLE_TRANSFER_SECONDS, 0.14);
  let visibleFrames = 0;
  while (strideCradleTransferProgress(state) !== null && visibleFrames < 10) {
    visibleFrames += 1;
    stepStrideCradle(state, 1 / 30, 0.8);
  }
  assert.ok(visibleFrames >= 4 && visibleFrames <= 5);
});

test('contact tint crossfades continuously between adjacent masses', () => {
  const state = createStrideCradleState(0);
  state.transferDirection = 1;
  for (const progress of [0, 0.1, 0.25, 0.5, 0.75, 0.9]) {
    state.transferAge = progress * STRIDE_CRADLE_TRANSFER_SECONDS;
    const weights = strideCradleContactWeights(state, 6);
    const active = weights.filter((weight) => weight > 0);
    assert.ok(active.length >= 1 && active.length <= 2);
    assert.ok(Math.abs(weights.reduce((sum, weight) => sum + weight, 0) - 1) < 1e-9);
  }

  state.transferAge = STRIDE_CRADLE_TRANSFER_SECONDS * 0.1;
  assert.deepEqual(strideCradleContactWeights(state, 6), [0.5, 0.5, 0, 0, 0, 0]);
  state.transferDirection = -1;
  assert.deepEqual(strideCradleContactWeights(state, 6), [0, 0, 0, 0, 0.5, 0.5]);
});

test('the returning right mass transfers momentum back to the far left', () => {
  const state = createStrideCradleState(0.8);
  let sawRightImpact = false;
  for (let elapsed = 0; elapsed < 2; elapsed += 1 / 240) {
    stepStrideCradle(state, 1 / 240, 0.8);
    if (state.transferDirection === -1 && state.transferAge < 0.01) {
      sawRightImpact = true;
      break;
    }
  }
  assert.equal(sawRightImpact, true);
  assert.equal(state.rightAngle, 0);
  assert.equal(state.rightVelocity, 0);
  assert.ok(state.leftVelocity < 0);
});

test('audio is an external force: louder input produces more excursion', () => {
  const quiet = createStrideCradleState(0.1);
  const loud = createStrideCradleState(1);
  assert.ok(Math.abs(loud.leftAngle) > Math.abs(quiet.leftAngle));
});

test('an open but silent microphone does not invent momentum', () => {
  const state = createStrideCradleState(0);
  stepStrideCradle(state, 1, 0);
  assert.deepEqual(
    [state.leftAngle, state.leftVelocity, state.rightAngle, state.rightVelocity],
    [0, 0, 0, 0],
  );
});

test('changing the measured source does not teleport an active flight', () => {
  const state = createStrideCradleState(0.8, 'human');
  stepStrideCradle(state, 0.1, 0.8, 'human');
  const leftBeforeSourceChange = state.leftAngle;
  stepStrideCradle(state, 1 / 240, 0.8, 'agent');
  assert.ok(state.leftAngle > leftBeforeSourceChange);
  assert.equal(state.rightAngle, 0);
  assert.equal(state.rightVelocity, 0);
});

test('impact drive uses angular velocity and sustains the requested energy', () => {
  const level = 0.75;
  const state = createStrideCradleState(level);
  const releasedAngle = Math.abs(state.leftAngle);
  let farRightApex = 0;
  for (let elapsed = 0; elapsed < 1; elapsed += 1 / 240) {
    stepStrideCradle(state, 1 / 240, level);
    farRightApex = Math.max(farRightApex, state.rightAngle);
  }
  assert.ok(farRightApex > releasedAngle * 0.9);
  assert.ok(farRightApex < releasedAngle * 1.05);
});
