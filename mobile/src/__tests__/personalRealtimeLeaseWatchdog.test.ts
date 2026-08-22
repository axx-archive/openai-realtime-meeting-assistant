import assert from 'node:assert/strict';
import test from 'node:test';
import {
  PERSONAL_REALTIME_LEASE_TEARDOWN_RESERVE_MS,
  PersonalRealtimeLeaseWatchdog,
  personalRealtimeLeaseTiming,
  type PersonalRealtimeLeaseScheduler,
} from '../realtime/personalRealtimeLeaseWatchdog';
import { closePersonalRealtimeTransportResources } from '../realtime/personalRealtimeTerminal';

type FakeTimer = ReturnType<typeof setTimeout>;

class FakeLeaseScheduler implements PersonalRealtimeLeaseScheduler {
  private nextId = 1;
  private readonly active = new Map<number, { at: number; callback: () => void }>();
  private readonly all = new Map<number, () => void>();

  constructor(private currentMs: number) {}

  now(): number {
    return this.currentMs;
  }

  setTimeout(callback: () => void, delayMs: number): FakeTimer {
    const id = this.nextId;
    this.nextId += 1;
    this.active.set(id, { at: this.currentMs + delayMs, callback });
    this.all.set(id, callback);
    return id as unknown as FakeTimer;
  }

  clearTimeout(timer: FakeTimer): void {
    this.active.delete(timer as unknown as number);
  }

  advanceBy(deltaMs: number): void {
    this.currentMs += deltaMs;
    const due = [...this.active.entries()]
      .filter(([, task]) => task.at <= this.currentMs)
      .sort((left, right) => left[1].at - right[1].at);
    for (const [id, task] of due) {
      if (!this.active.delete(id)) continue;
      task.callback();
    }
  }

  invokeEvenIfCleared(id: number): void {
    this.all.get(id)?.();
  }
}

test('lease timing bounds renewal before the teardown watchdog and server expiry', () => {
  const nowMs = Date.parse('2026-08-22T12:00:00.000Z');
  const expiresAt = new Date(nowMs + 30_000).toISOString();
  const timing = personalRealtimeLeaseTiming(expiresAt, nowMs);
  assert.ok(timing);
  assert.equal(timing.watchdogDelayMs, 27_000);
  assert.equal(timing.renewRequestTimeoutMs, 5_000);
  assert.equal(timing.teardownAtMs, timing.expiresAtMs - PERSONAL_REALTIME_LEASE_TEARDOWN_RESERVE_MS);
  assert.ok(timing.renewRequestTimeoutMs < timing.watchdogDelayMs);
  assert.ok(timing.watchdogDelayMs < timing.expiresAtMs - nowMs);
  assert.equal(personalRealtimeLeaseTiming(new Date(nowMs + 3_001).toISOString(), nowMs), null);
});

test('a never-resolving renewal cannot keep the exact-generation peer or microphone past the local bound', async () => {
  const nowMs = Date.parse('2026-08-22T12:00:00.000Z');
  const scheduler = new FakeLeaseScheduler(nowMs);
  const watchdog = new PersonalRealtimeLeaseWatchdog(scheduler);
  let peerClosed = false;
  let trackStopped = false;
  let teardownVisible = false;
  let renewalSettled = false;
  const neverResolvingRenewal = new Promise<{ leaseExpiresAt: string }>(() => undefined);
  void neverResolvingRenewal.then(() => { renewalSettled = true; });
  const neverResolvingNativeDeactivation = new Promise<void>(() => undefined);

  const timing = watchdog.arm({
    connectionGeneration: 14,
    leaseGeneration: 3,
    leaseToken: 'lease-token-3',
    leaseExpiresAt: new Date(nowMs + 30_000).toISOString(),
  }, (deadline) => {
    assert.deepEqual(deadline, {
      connectionGeneration: 14,
      leaseGeneration: 3,
      leaseToken: 'lease-token-3',
      leaseExpiresAt: new Date(nowMs + 30_000).toISOString(),
    });
    teardownVisible = true;
    void closePersonalRealtimeTransportResources({
      dataChannel: null,
      peer: {
        close: () => { peerClosed = true; },
      },
      stream: {
        getTracks: () => [{ stop: () => { trackStopped = true; } }],
      },
      deactivateMediaSession: () => neverResolvingNativeDeactivation,
    });
  });
  assert.ok(timing);

  scheduler.advanceBy(timing.watchdogDelayMs - 1);
  assert.deepEqual({ renewalSettled, teardownVisible, peerClosed, trackStopped }, {
    renewalSettled: false,
    teardownVisible: false,
    peerClosed: false,
    trackStopped: false,
  });
  scheduler.advanceBy(1);
  assert.deepEqual({ renewalSettled, teardownVisible, peerClosed, trackStopped }, {
    renewalSettled: false,
    teardownVisible: true,
    peerClosed: true,
    trackStopped: true,
  });
  assert.equal(scheduler.now(), nowMs + 30_000 - PERSONAL_REALTIME_LEASE_TEARDOWN_RESERVE_MS);
});

test('re-arming advances the watchdog epoch so an already-queued prior-generation callback is inert', () => {
  const nowMs = Date.parse('2026-08-22T12:00:00.000Z');
  const scheduler = new FakeLeaseScheduler(nowMs);
  const watchdog = new PersonalRealtimeLeaseWatchdog(scheduler);
  const deadlines: number[] = [];
  watchdog.arm({
    connectionGeneration: 8,
    leaseGeneration: 1,
    leaseToken: 'old',
    leaseExpiresAt: new Date(nowMs + 30_000).toISOString(),
  }, (snapshot) => deadlines.push(snapshot.connectionGeneration));
  watchdog.arm({
    connectionGeneration: 9,
    leaseGeneration: 2,
    leaseToken: 'new',
    leaseExpiresAt: new Date(nowMs + 40_000).toISOString(),
  }, (snapshot) => deadlines.push(snapshot.connectionGeneration));

  scheduler.invokeEvenIfCleared(1);
  assert.deepEqual(deadlines, []);
  scheduler.advanceBy(37_000);
  assert.deepEqual(deadlines, [9]);
});
