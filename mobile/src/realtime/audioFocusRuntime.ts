import { AudioFocusCoordinator } from '../voice/AudioFocusCoordinator';

/**
 * Process-wide foreground microphone arbiter. Screen-local state may mount and
 * unmount while a room is joining, so focus ownership cannot live in one
 * screen's hook state.
 */
export const audioFocusRuntime = new AudioFocusCoordinator();
