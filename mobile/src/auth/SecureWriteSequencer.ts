export type SecureValue = string | null;

type DesiredWrite = {
  version: number;
  value: SecureValue;
  isCurrent: () => boolean;
};

type KeyLane = {
  gate: Promise<void>;
  desired: DesiredWrite | null;
};

function settleWithin(work: Promise<unknown>, timeoutMs: number): Promise<void> {
  return new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve();
    };
    const timer = setTimeout(finish, timeoutMs);
    void work.then(finish, finish);
  });
}

/**
 * Serializes SecureStore intent without letting a hung native promise poison
 * every future account. A timed-out attempt may keep running, so its eventual
 * settlement reconciles the newest desired value. That makes both cases safe:
 * a never-settling A delete cannot block B, and a late-settling A delete is
 * followed by a B rewrite.
 */
export class SecureWriteSequencer {
  private readonly lanes = new Map<string, KeyLane>();

  private nextVersion = 0;

  constructor(
    private readonly writer: (key: string, value: SecureValue) => Promise<void>,
    private readonly ownershipTimeoutMs: number,
  ) {}

  write(
    key: string,
    value: SecureValue,
    isCurrent: () => boolean,
  ): Promise<boolean> {
    if (!isCurrent()) return Promise.resolve(false);
    let lane = this.lanes.get(key);
    if (!lane) {
      lane = { gate: Promise.resolve(), desired: null };
      this.lanes.set(key, lane);
    }
    const desired: DesiredWrite = {
      version: ++this.nextVersion,
      value,
      isCurrent,
    };
    lane.desired = desired;
    const predecessor = lane.gate;
    let release!: () => void;
    lane.gate = new Promise<void>((resolve) => { release = resolve; });

    return (async () => {
      await settleWithin(predecessor, this.ownershipTimeoutMs);
      if (lane?.desired !== desired || !desired.isCurrent()) {
        release();
        return false;
      }

      const attempt = Promise.resolve().then(() => this.writer(key, value));
      void attempt.then(
        () => this.reconcileAfterSettlement(key, desired.version),
        () => this.reconcileAfterSettlement(key, desired.version),
      );
      await settleWithin(attempt, this.ownershipTimeoutMs);
      release();
      return lane?.desired === desired && desired.isCurrent();
    })();
  }

  private reconcileAfterSettlement(key: string, settledVersion: number): void {
    const desired = this.lanes.get(key)?.desired;
    if (!desired || desired.version === settledVersion || !desired.isCurrent()) return;
    const attempt = Promise.resolve().then(() => this.writer(key, desired.value));
    void attempt.then(
      () => this.reconcileAfterSettlement(key, desired.version),
      () => undefined,
    );
  }
}
