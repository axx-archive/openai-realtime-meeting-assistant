export type PushBindingAuthority = {
  accountKey: string;
  sessionToken: string;
  token: string;
};

export type PushBindingHandlers = {
  register: (authority: PushBindingAuthority) => Promise<unknown>;
  unregister: (authority: PushBindingAuthority) => Promise<unknown>;
  onRegistered?: (authority: PushBindingAuthority) => Promise<unknown> | unknown;
  onRetired?: (authority: PushBindingAuthority) => Promise<unknown> | unknown;
};

type BindingEntry = {
  authority: PushBindingAuthority;
  handlers: PushBindingHandlers;
};

type TokenLane = {
  desired: BindingEntry | null;
  desiredEstablished: boolean;
  retired: Map<string, BindingEntry>;
  retirementWaiters: Map<string, Set<(value: boolean) => void>>;
  revision: number;
  tail: Promise<void>;
  reconcileQueued: boolean;
  unsettled: Map<Promise<unknown>, { kind: 'register' | 'unregister'; entry: BindingEntry }>;
  retryTimer: ReturnType<typeof setTimeout> | null;
};

type OwnedResult = 'succeeded' | 'failed' | 'timed_out';

function normalizedAuthority(authority: PushBindingAuthority): PushBindingAuthority | null {
  const accountKey = authority.accountKey.trim().toLowerCase();
  const sessionToken = authority.sessionToken.trim();
  const token = authority.token.trim();
  if (!accountKey || !sessionToken || !token) return null;
  return { accountKey, sessionToken, token };
}

function authorityID(authority: PushBindingAuthority): string {
  return `${authority.accountKey}\u0000${authority.sessionToken}\u0000${authority.token}`;
}

/**
 * One process-wide mutation lane per Expo token.
 *
 * Expo tokens are server-side last-writer bindings, so an old account POST and
 * a replacement account POST must never be treated as unrelated requests. A
 * network promise can also remain pending after the server committed it. Each
 * operation therefore has bounded queue ownership, while its eventual result
 * stays observed: any late result invalidates the assumed final writer and
 * causes the current desired owner to be reasserted. Failed retirement never
 * blocks that reassertion and remains on an in-process retry loop.
 */
export class PushBindingCoordinator {
  private readonly lanes = new Map<string, TokenLane>();

  private disposed = false;

  constructor(
    private readonly ownershipTimeoutMs = 2_000,
    private readonly retryDelayMs = 5_000,
  ) {}

  setDesired(
    authority: PushBindingAuthority,
    handlers: PushBindingHandlers,
  ): Promise<void> {
    const normalized = normalizedAuthority(authority);
    if (!normalized || this.disposed) return Promise.resolve();
    const lane = this.lane(normalized.token);
    const next: BindingEntry = { authority: normalized, handlers };
    const nextID = authorityID(normalized);
    const previous = lane.desired;
    if (previous && authorityID(previous.authority) !== nextID) {
      lane.retired.set(authorityID(previous.authority), previous);
    }
    lane.retired.delete(nextID);
    lane.desired = next;
    lane.desiredEstablished = false;
    lane.revision += 1;
    return this.requestReconcile(normalized.token, lane);
  }

  retire(
    authority: PushBindingAuthority,
    handlers: PushBindingHandlers,
  ): Promise<boolean> {
    const normalized = normalizedAuthority(authority);
    if (!normalized || this.disposed) return Promise.resolve(false);
    const lane = this.lane(normalized.token);
    const id = authorityID(normalized);
    if (lane.desired && authorityID(lane.desired.authority) === id) {
      lane.desired = null;
      lane.desiredEstablished = false;
    }
    lane.retired.set(id, { authority: normalized, handlers });
    lane.revision += 1;
    const retired = new Promise<boolean>((resolve) => {
      const waiters = lane.retirementWaiters.get(id) ?? new Set();
      waiters.add(resolve);
      lane.retirementWaiters.set(id, waiters);
    });
    void this.requestReconcile(normalized.token, lane);
    return retired;
  }

  dispose(): void {
    this.disposed = true;
    for (const lane of this.lanes.values()) {
      if (lane.retryTimer) clearTimeout(lane.retryTimer);
      for (const waiters of lane.retirementWaiters.values()) {
        for (const resolve of waiters) resolve(false);
      }
      lane.retirementWaiters.clear();
    }
    this.lanes.clear();
  }

  private lane(token: string): TokenLane {
    const existing = this.lanes.get(token);
    if (existing) return existing;
    const created: TokenLane = {
      desired: null,
      desiredEstablished: false,
      retired: new Map(),
      retirementWaiters: new Map(),
      revision: 0,
      tail: Promise.resolve(),
      reconcileQueued: false,
      unsettled: new Map(),
      retryTimer: null,
    };
    this.lanes.set(token, created);
    return created;
  }

  private requestReconcile(token: string, lane: TokenLane): Promise<void> {
    if (this.disposed) return Promise.resolve();
    if (lane.retryTimer) {
      clearTimeout(lane.retryTimer);
      lane.retryTimer = null;
    }
    if (lane.reconcileQueued) return lane.tail;
    lane.reconcileQueued = true;
    const next = lane.tail
      .catch(() => undefined)
      .then(async () => {
        lane.reconcileQueued = false;
        await this.reconcile(token, lane);
      });
    lane.tail = next;
    return next;
  }

  private async reconcile(token: string, lane: TokenLane): Promise<void> {
    if (this.disposed) return;
    const startedRevision = lane.revision;

    // Retirement is attempted first, but a failure never prevents the desired
    // owner from being written last in this pass.
    for (const [id, entry] of [...lane.retired.entries()]) {
      if (lane.retired.get(id) !== entry) continue;
      const result = await this.runOwned(token, lane, 'unregister', entry);
      if (result !== 'succeeded' || lane.retired.get(id) !== entry) continue;
      if (this.hasUnsettledRegistration(lane, id)) {
        // Do not revoke the only credential capable of deleting this binding
        // while an already-authenticated POST can still commit afterward. The
        // lane keeps deleting/retrying in-process; final logout happens only
        // after that uncertain writer settles and one last DELETE succeeds.
        continue;
      }
      try {
        await entry.handlers.onRetired?.(entry.authority);
      } catch {
        continue;
      }
      lane.retired.delete(id);
      const waiters = lane.retirementWaiters.get(id);
      if (waiters) {
        lane.retirementWaiters.delete(id);
        for (const resolve of waiters) resolve(true);
      }
      // A same-account old-session DELETE can remove the token that a newer
      // session just installed, so every successful retirement invalidates the
      // desired-owner proof until it is written again below.
      lane.desiredEstablished = false;
    }

    const desired = lane.desired;
    if (desired) {
      const result = await this.runOwned(token, lane, 'register', desired);
      if (result === 'succeeded' && lane.desired === desired) {
        try {
          await desired.handlers.onRegistered?.(desired.authority);
          lane.desiredEstablished = true;
        } catch {
          lane.desiredEstablished = false;
        }
      } else {
        lane.desiredEstablished = false;
        if (result === 'succeeded' && lane.desired !== desired) {
          lane.retired.set(authorityID(desired.authority), desired);
        }
      }
    }

    if (lane.revision !== startedRevision) {
      void this.requestReconcile(token, lane);
      return;
    }
    if (
      lane.retired.size > 0
      || lane.unsettled.size > 0
      || (lane.desired !== null && !lane.desiredEstablished)
    ) {
      this.scheduleRetry(token, lane);
    }
  }

  private async runOwned(
    token: string,
    lane: TokenLane,
    kind: 'register' | 'unregister',
    entry: BindingEntry,
  ): Promise<OwnedResult> {
    const work = Promise.resolve().then(() => (
      kind === 'register'
        ? entry.handlers.register(entry.authority)
        : entry.handlers.unregister(entry.authority)
    ));
    const observed = work.then<OwnedResult, OwnedResult>(
      () => 'succeeded',
      () => 'failed',
    );
    let timer: ReturnType<typeof setTimeout> | null = null;
    const result = await Promise.race<OwnedResult>([
      observed,
      new Promise<OwnedResult>((resolve) => {
        timer = setTimeout(() => resolve('timed_out'), this.ownershipTimeoutMs);
      }),
    ]);
    if (timer) clearTimeout(timer);
    if (result !== 'timed_out') return result;

    lane.unsettled.set(work, { kind, entry });
    // The timed-out request may still commit at the server. Its late success or
    // failure is an ordering event, not something we can discard: enqueue a
    // fresh pass that writes the current desired authority last.
    void observed.then((lateResult) => {
      lane.unsettled.delete(work);
      if (kind === 'register' && lateResult === 'succeeded') {
        const desiredID = lane.desired ? authorityID(lane.desired.authority) : null;
        const lateID = authorityID(entry.authority);
        if (desiredID !== lateID) {
          // A retirement may already have succeeded while this old POST still
          // owned a server request. Put its authority back on the lane so the
          // late last-writer is deterministically removed again.
          lane.retired.set(lateID, entry);
          lane.revision += 1;
        }
      }
      lane.desiredEstablished = false;
      void this.requestReconcile(token, lane);
    });
    return result;
  }

  private scheduleRetry(token: string, lane: TokenLane): void {
    if (this.disposed || lane.retryTimer) return;
    lane.retryTimer = setTimeout(() => {
      lane.retryTimer = null;
      void this.requestReconcile(token, lane);
    }, this.retryDelayMs);
  }

  private hasUnsettledRegistration(lane: TokenLane, id: string): boolean {
    for (const unsettled of lane.unsettled.values()) {
      if (
        unsettled.kind === 'register'
        && authorityID(unsettled.entry.authority) === id
      ) return true;
    }
    return false;
  }
}

export const pushBindingCoordinator = new PushBindingCoordinator();
