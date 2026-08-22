export const PERSONAL_REALTIME_LEASE_TEARDOWN_RESERVE_MS = 3_000;
export const PERSONAL_REALTIME_LEASE_RENEW_REQUEST_MAX_MS = 5_000;

export type PersonalRealtimeLeaseTiming = {
  expiresAtMs: number;
  teardownAtMs: number;
  watchdogDelayMs: number;
  renewRequestTimeoutMs: number;
};

export type PersonalRealtimeLeaseWatchdogSnapshot = {
  connectionGeneration: number;
  leaseGeneration: number;
  leaseToken: string;
  leaseExpiresAt: string;
};

type PersonalRealtimeLeaseTimer = ReturnType<typeof setTimeout>;

export type PersonalRealtimeLeaseScheduler = {
  now(): number;
  setTimeout(callback: () => void, delayMs: number): PersonalRealtimeLeaseTimer;
  clearTimeout(timer: PersonalRealtimeLeaseTimer): void;
};

const runtimeScheduler: PersonalRealtimeLeaseScheduler = {
  now: () => Date.now(),
  setTimeout: (callback, delayMs) => setTimeout(callback, delayMs),
  clearTimeout: (timer) => clearTimeout(timer),
};

/**
 * Convert the server's absolute lease authority into two local deadlines. The
 * watchdog begins terminal teardown three seconds before server expiry, while
 * every renewal request must settle at least one millisecond before that
 * watchdog. A missing/expired/too-short lease is never admitted.
 */
export function personalRealtimeLeaseTiming(
  leaseExpiresAt: string,
  nowMs: number,
  teardownReserveMs = PERSONAL_REALTIME_LEASE_TEARDOWN_RESERVE_MS,
  renewRequestMaxMs = PERSONAL_REALTIME_LEASE_RENEW_REQUEST_MAX_MS,
): PersonalRealtimeLeaseTiming | null {
  const expiresAtMs = Date.parse(leaseExpiresAt);
  if (
    !Number.isFinite(expiresAtMs)
    || !Number.isFinite(nowMs)
    || !Number.isFinite(teardownReserveMs)
    || teardownReserveMs < 1
    || !Number.isFinite(renewRequestMaxMs)
    || renewRequestMaxMs < 1
  ) return null;
  const teardownAtMs = expiresAtMs - teardownReserveMs;
  const watchdogDelayMs = teardownAtMs - nowMs;
  if (watchdogDelayMs <= 1) return null;
  return {
    expiresAtMs,
    teardownAtMs,
    watchdogDelayMs,
    renewRequestTimeoutMs: Math.min(renewRequestMaxMs, watchdogDelayMs - 1),
  };
}

/**
 * One exact-generation watchdog owns the current server lease. Re-arming or
 * clearing advances a local epoch so even an already-queued stale callback is
 * inert. It remains independent of the renewal promise by design: a provider,
 * fetch, or test double that never settles cannot keep the microphone alive.
 */
export class PersonalRealtimeLeaseWatchdog {
  private epoch = 0;
  private timer: PersonalRealtimeLeaseTimer | null = null;

  constructor(private readonly scheduler: PersonalRealtimeLeaseScheduler = runtimeScheduler) {}

  now(): number {
    return this.scheduler.now();
  }

  arm(
    snapshot: PersonalRealtimeLeaseWatchdogSnapshot,
    onDeadline: (snapshot: PersonalRealtimeLeaseWatchdogSnapshot) => void,
  ): PersonalRealtimeLeaseTiming | null {
    this.clear();
    const timing = personalRealtimeLeaseTiming(snapshot.leaseExpiresAt, this.scheduler.now());
    if (!timing) return null;
    const epoch = this.epoch;
    this.timer = this.scheduler.setTimeout(() => {
      if (this.epoch !== epoch) return;
      this.timer = null;
      onDeadline(snapshot);
    }, timing.watchdogDelayMs);
    return timing;
  }

  clear(): void {
    this.epoch += 1;
    if (this.timer !== null) this.scheduler.clearTimeout(this.timer);
    this.timer = null;
  }
}
