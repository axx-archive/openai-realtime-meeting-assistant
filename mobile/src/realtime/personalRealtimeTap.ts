import type { AudioFocusTerminalReason } from '../voice/AudioFocusCoordinator';
import type { PersonalRealtimeStatus } from './personalRealtimeProtocol';

export type PersonalRealtimeTapController = {
  enabled: boolean;
  active: boolean;
  status: PersonalRealtimeStatus;
  start: () => Promise<void>;
  stop: (reason: AudioFocusTerminalReason) => Promise<void>;
};

export type PersonalRealtimeTapOutcome = 'disabled' | 'started' | 'stopped';

// The Home waveform has one action contract. In particular, an errored
// transport must be closed before a new start; otherwise the status ref can
// reject the tap and make a visibly enabled control appear inert.
export async function runPersonalRealtimeTap(
  realtime: PersonalRealtimeTapController,
): Promise<PersonalRealtimeTapOutcome> {
  if (!realtime.enabled) return 'disabled';
  if (realtime.active) {
    await realtime.stop('completed');
    return 'stopped';
  }
  if (realtime.status === 'error') await realtime.stop('cancelled');
  await realtime.start();
  return 'started';
}
