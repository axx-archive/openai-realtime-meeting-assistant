import type { AppStateStatus } from 'react-native';
import type { PersonalRealtimeStatus } from './personalRealtimeProtocol';

export type PersonalRealtimeAppLifecycleAction = 'retain' | 'stop';

/**
 * AppState can report more than one terminal foreground transition before an
 * asynchronous native teardown settles (`unknown` then `background`, for
 * example). This latch gives the whole transition chain one close operation.
 */
export class PersonalRealtimeTerminalLatch {
  private inFlight: Promise<void> | null = null;
  private latched = false;
  private rearmAfterSettle = false;

  run(close: () => Promise<void>): Promise<void> {
    if (this.latched) return this.inFlight ?? Promise.resolve();
    this.latched = true;
    let operation: Promise<void>;
    operation = Promise.resolve()
      .then(close)
      .finally(() => {
        if (this.inFlight !== operation) return;
        this.inFlight = null;
        if (this.rearmAfterSettle) {
          this.rearmAfterSettle = false;
          this.latched = false;
        }
      });
    this.inFlight = operation;
    return operation;
  }

  /** A new foreground epoch may own one new terminal transition. */
  rearm(): void {
    if (this.inFlight) {
      this.rearmAfterSettle = true;
      return;
    }
    this.latched = false;
  }
}

/**
 * Personal Scout voice is allowed to survive ordinary iOS interruptions and
 * route changes, but never an actual background boundary. `inactive` is a
 * transient foreground state used by permission sheets, Control Center and
 * system interruptions; stopping there makes the microphone look broken.
 * Backgrounding is different: the app is no longer visible, so private capture
 * is ended exactly once and must be explicitly restarted after foregrounding.
 */
export function personalRealtimeAppLifecycleAction(
  previous: AppStateStatus,
  next: AppStateStatus,
  realtimeStatus: PersonalRealtimeStatus,
): PersonalRealtimeAppLifecycleAction {
  if (previous === next || realtimeStatus === 'idle' || realtimeStatus === 'error') {
    return 'retain';
  }
  return next === 'background' || next === 'unknown' || next === 'extension'
    ? 'stop'
    : 'retain';
}
