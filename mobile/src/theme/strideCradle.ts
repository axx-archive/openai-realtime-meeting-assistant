/**
 * Hybrid Newton's-cradle model for two equal edge masses and a rigid line of
 * three equal centre masses.
 *
 * Between impacts each edge follows the nonlinear pendulum equation. At the
 * contact line the incoming mass stops and its velocity is transferred to the
 * opposite equal mass. The centres remain visually fixed: their Hertzian
 * compression is far shorter than a UI frame. `transferAge` exposes a short
 * perceptual trace of that impulse without changing the collision itself.
 */
const GRAVITY = 9.81;
const PENDULUM_LENGTH_METRES = 0.52;
const AIR_DAMPING = 0.045;
const RESTITUTION = 0.985;
const DRIVE_BLEND = 0.18;
const MIN_RELEASE_ANGLE = 0;
const MAX_RELEASE_ANGLE = Math.PI * 0.27;
const MAX_STEP_SECONDS = 1 / 120;
const ACTIVATION_LEVEL = 0.025;

export const STRIDE_CRADLE_TRANSFER_SECONDS = 0.26;
export type StrideCradleSource = 'human' | 'agent';

export type StrideCradleState = {
  leftAngle: number;
  leftVelocity: number;
  rightAngle: number;
  rightVelocity: number;
  transferDirection: -1 | 0 | 1;
  transferAge: number;
};

function clampLevel(level: number) {
  return Math.min(1, Math.max(0, level));
}

export function strideCradleReleaseAngle(level: number) {
  return MIN_RELEASE_ANGLE
    + (MAX_RELEASE_ANGLE - MIN_RELEASE_ANGLE) * clampLevel(level);
}

function targetImpactSpeed(level: number) {
  const angle = strideCradleReleaseAngle(level);
  return Math.sqrt(
    (2 * GRAVITY / PENDULUM_LENGTH_METRES) * (1 - Math.cos(angle)),
  );
}

export function createStrideCradleState(
  level: number,
  source: StrideCradleSource = 'human',
): StrideCradleState {
  const release = strideCradleReleaseAngle(level);
  return {
    leftAngle: source === 'human' ? -release : 0,
    leftVelocity: 0,
    rightAngle: source === 'agent' ? release : 0,
    rightVelocity: 0,
    transferDirection: 0,
    transferAge: STRIDE_CRADLE_TRANSFER_SECONDS,
  };
}

function drivenImpactSpeed(incomingSpeed: number, level: number) {
  const conserved = incomingSpeed * RESTITUTION;
  const driven = targetImpactSpeed(level);
  // The microphone is an external force on this visual system. It adds or
  // removes energy only at contact; the flight between contacts stays physical.
  return conserved + (driven - conserved) * DRIVE_BLEND;
}

function integrateStep(state: StrideCradleState, dt: number, level: number) {
  const leftAcceleration =
    -(GRAVITY / PENDULUM_LENGTH_METRES) * Math.sin(state.leftAngle)
    - AIR_DAMPING * state.leftVelocity;
  state.leftVelocity += leftAcceleration * dt;
  state.leftAngle += state.leftVelocity * dt;

  const rightAcceleration =
    -(GRAVITY / PENDULUM_LENGTH_METRES) * Math.sin(state.rightAngle)
    - AIR_DAMPING * state.rightVelocity;
  state.rightVelocity += rightAcceleration * dt;
  state.rightAngle += state.rightVelocity * dt;

  if (state.leftAngle >= 0 && state.leftVelocity > 0) {
    const outgoing = drivenImpactSpeed(state.leftVelocity, level);
    state.leftAngle = 0;
    state.leftVelocity = 0;
    state.rightAngle = 0;
    state.rightVelocity = outgoing;
    state.transferDirection = 1;
    state.transferAge = 0;
  } else if (state.rightAngle <= 0 && state.rightVelocity < 0) {
    const outgoing = drivenImpactSpeed(-state.rightVelocity, level);
    state.rightAngle = 0;
    state.rightVelocity = 0;
    state.leftAngle = 0;
    state.leftVelocity = -outgoing;
    state.transferDirection = -1;
    state.transferAge = 0;
  }

  state.transferAge += dt;
}

export function stepStrideCradle(
  state: StrideCradleState,
  elapsedSeconds: number,
  level: number,
  source: StrideCradleSource = 'human',
) {
  const dormant = Math.abs(state.leftAngle) < 0.001
    && Math.abs(state.leftVelocity) < 0.001
    && Math.abs(state.rightAngle) < 0.001
    && Math.abs(state.rightVelocity) < 0.001;
  if (dormant && level > ACTIVATION_LEVEL) {
    if (source === 'agent') {
      state.rightAngle = strideCradleReleaseAngle(level);
    } else {
      state.leftAngle = -strideCradleReleaseAngle(level);
    }
  }
  let remaining = Math.min(0.1, Math.max(0, elapsedSeconds));
  while (remaining > 0) {
    const step = Math.min(MAX_STEP_SECONDS, remaining);
    integrateStep(state, step, level);
    remaining -= step;
  }
  if (
    Math.abs(state.leftAngle) < 0.0001
    && Math.abs(state.leftVelocity) < 0.0001
    && Math.abs(state.rightAngle) < 0.0001
    && Math.abs(state.rightVelocity) < 0.0001
  ) {
    state.leftAngle = 0;
    state.leftVelocity = 0;
    state.rightAngle = 0;
    state.rightVelocity = 0;
  }
  return state;
}

export function strideCradleTransferProgress(state: StrideCradleState) {
  if (
    state.transferDirection === 0
    || state.transferAge >= STRIDE_CRADLE_TRANSFER_SECONDS
  ) {
    return null;
  }
  return state.transferAge / STRIDE_CRADLE_TRANSFER_SECONDS;
}

export function strideCradleContactWeights(
  state: StrideCradleState,
  ballCount: number,
) {
  const count = Math.max(2, Math.floor(ballCount));
  const weights = Array.from({ length: count }, () => 0);
  const progress = strideCradleTransferProgress(state);
  if (progress === null) return weights;

  const position = state.transferDirection > 0
    ? progress * (count - 1)
    : (1 - progress) * (count - 1);
  // A localized cosine pulse makes each mass visibly receive the impulse in
  // sequence. This is a luminance trace only: the centre masses stay fixed and
  // the equal-mass velocity transfer remains instantaneous in the physics.
  const pulseRadius = 0.72;
  for (let index = 0; index < count; index += 1) {
    const distance = Math.abs(position - index);
    if (distance >= pulseRadius) continue;
    const phase = distance / pulseRadius;
    weights[index] = Math.pow(0.5 + 0.5 * Math.cos(Math.PI * phase), 0.72);
  }
  return weights;
}
