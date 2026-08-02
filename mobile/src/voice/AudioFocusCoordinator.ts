/**
 * One foreground microphone owner per client. This is deliberately platform
 * neutral: room and Realtime adapters supply their own close/mute work, while
 * this coordinator owns ordering, generations, and stale completion fences.
 */
export type AudioFocusMode = 'idle' | 'composer_dictation' | 'personal_realtime' | 'meeting_media';

export type AudioFocusTerminalReason =
  | 'completed'
  | 'cancelled'
  | 'superseded_by_dictation'
  | 'superseded_by_personal_realtime'
  | 'superseded_by_meeting_media'
  | 'forced_close'
  | 'error';

export type AudioFocusCallbacks = {
  /** Stop/cancel underlying capture. It must resolve only after it is safe for the next owner to acquire. */
  forceClose?: (reason: AudioFocusTerminalReason) => void | Promise<void>;
  /** For private in-room dictation: captures and mutes the exact former room state. */
  parkRoomMute?: () => boolean | Promise<boolean>;
  /** Restores the exact value returned by parkRoomMute, never an assumed "unmuted" value. */
  restoreRoomMute?: (wasMuted: boolean) => void | Promise<void>;
};

export type AudioFocusLease = {
  readonly mode: Exclude<AudioFocusMode, 'idle'>;
  readonly generation: number;
  isCurrent(): boolean;
  release(reason?: AudioFocusTerminalReason): Promise<boolean>;
};

type ActiveFocus = {
  mode: Exclude<AudioFocusMode, 'idle'>;
  generation: number;
  callbacks: AudioFocusCallbacks;
  priorRoomMute?: boolean;
  closePromise?: Promise<void>;
};

type FocusIntent = {
  readonly id: number;
  owner: ActiveFocus | null;
  readonly reason: AudioFocusTerminalReason;
};

function supersessionReason(mode: Exclude<AudioFocusMode, 'idle'>): AudioFocusTerminalReason {
  switch (mode) {
    case 'composer_dictation': return 'superseded_by_dictation';
    case 'personal_realtime': return 'superseded_by_personal_realtime';
    case 'meeting_media': return 'superseded_by_meeting_media';
  }
}

/**
 * Serializes foreground audio transitions. A generation is invalidated before
 * a close callback runs, so a late callback or provider completion can never
 * reclaim focus after another mode won it.
 */
export class AudioFocusCoordinator {
  private active: ActiveFocus | null = null;
  /** Meeting media parked underneath a short composer-dictation lease. */
  private suspendedMeeting: ActiveFocus | null = null;
  private nextGeneration = 0;
  private nextIntent = 0;
  private latestIntent: FocusIntent | null = null;
  private queue: Promise<void> = Promise.resolve();

  get mode(): AudioFocusMode {
    return this.active && this.latestIntent?.owner === this.active ? this.active.mode : 'idle';
  }

  get generation(): number {
    return this.nextGeneration;
  }

  acquire(
    mode: Exclude<AudioFocusMode, 'idle'>,
    callbacks: AudioFocusCallbacks = {},
  ): Promise<AudioFocusLease> {
    const requested: ActiveFocus = {
      mode,
      generation: ++this.nextGeneration,
      callbacks,
    };
    const intent: FocusIntent = {
      id: ++this.nextIntent,
      owner: requested,
      reason: supersessionReason(mode),
    };
    // This is the linearization point. Existing leases become stale before any
    // user callback is awaited, while `active` remains reachable by the queued
    // transition that is responsible for closing it exactly once.
    this.latestIntent = intent;
    const lease = this.leaseFor(requested);
    return this.enqueue(async () => {
      try {
        if (this.latestIntent !== intent) {
          await this.close(requested, this.latestSupersessionReason(mode));
          return lease;
        }

        const previous = this.active;
        if (previous) this.active = null;

        if (previous?.mode === 'meeting_media' && mode === 'composer_dictation') {
          await this.parkMeetingForComposer(previous, requested);
        } else {
          if (previous) await this.close(previous, supersessionReason(mode));
          if (mode === 'composer_dictation' && this.suspendedMeeting) {
            // A prior composer was superseded while a meeting was parked. Its
            // close restored the room, so park that same room for the new clip.
            await this.parkMeetingForComposer(this.suspendedMeeting, requested);
          } else if (this.suspendedMeeting) {
            const suspended = this.suspendedMeeting;
            this.suspendedMeeting = null;
            await this.close(suspended, supersessionReason(mode));
          } else if (mode === 'composer_dictation' && callbacks.parkRoomMute) {
            requested.priorRoomMute = await callbacks.parkRoomMute();
          }
        }

        if (this.latestIntent !== intent) {
          await this.close(requested, this.latestSupersessionReason(mode));
          return lease;
        }
        this.active = requested;
        return lease;
      } catch (error) {
        // A predecessor teardown or room-park failure means this request was
        // never safely grantable. Fence it, close its pending hooks once, and
        // leave the queue usable for the next intent.
        if (this.latestIntent === intent) {
          this.latestIntent = {
            id: ++this.nextIntent,
            owner: null,
            reason: 'error',
          };
          ++this.nextGeneration;
          if (this.suspendedMeeting) {
            const suspended = this.suspendedMeeting;
            this.suspendedMeeting = null;
            try { await this.close(suspended, 'error'); } catch { /* Preserve the transition error. */ }
          }
        }
        try { await this.close(requested, 'error'); } catch { /* Preserve the transition error. */ }
        throw error;
      }
    });
  }

  async forceClose(reason: AudioFocusTerminalReason = 'forced_close'): Promise<void> {
    const intent: FocusIntent = {
      id: ++this.nextIntent,
      owner: null,
      reason,
    };
    this.latestIntent = intent;
    ++this.nextGeneration;
    return this.enqueue(async () => {
      const previous = this.active;
      this.active = null;
      if (previous) await this.close(previous, reason);
      if (this.suspendedMeeting) {
        const suspended = this.suspendedMeeting;
        this.suspendedMeeting = null;
        await this.close(suspended, reason);
      }
    });
  }

  private leaseFor(active: ActiveFocus): AudioFocusLease {
    return {
      mode: active.mode,
      generation: active.generation,
      isCurrent: () => this.active === active && this.latestIntent?.owner === active,
      release: async (reason: AudioFocusTerminalReason = 'completed') => {
        const isActive = this.active === active && this.latestIntent?.owner === active;
        const isSuspended = this.suspendedMeeting === active;
        if (!isActive && !isSuspended) return false;
        const intent: FocusIntent = {
          id: ++this.nextIntent,
          owner: null,
          reason,
        };
        this.latestIntent = intent;
        ++this.nextGeneration;
        return this.enqueue(async () => {
          if (this.suspendedMeeting === active) {
            const composer = this.active?.mode === 'composer_dictation' ? this.active : null;
            this.active = null;
            this.suspendedMeeting = null;
            if (composer) await this.close(composer, 'forced_close');
            await this.close(active, reason);
            return true;
          }
          if (this.active !== active) return false;
          this.active = null;
          await this.close(active, reason);
          if (active.mode === 'composer_dictation' && this.suspendedMeeting && this.latestIntent === intent) {
            this.active = this.suspendedMeeting;
            this.suspendedMeeting = null;
            intent.owner = this.active;
          }
          return true;
        });
      },
    };
  }

  private enqueue<T>(work: () => Promise<T>): Promise<T> {
    const operation = this.queue.then(work);
    // A failing device callback must reject its own operation without poisoning
    // every later focus transition.
    this.queue = operation.then(() => undefined, () => undefined);
    return operation;
  }

  private latestSupersessionReason(
    fallbackMode: Exclude<AudioFocusMode, 'idle'>,
  ): AudioFocusTerminalReason {
    const latest = this.latestIntent;
    if (latest?.owner) return supersessionReason(latest.owner.mode);
    return latest?.reason ?? supersessionReason(fallbackMode);
  }

  private async parkMeetingForComposer(meeting: ActiveFocus, composer: ActiveFocus): Promise<void> {
    this.suspendedMeeting = meeting;
    composer.callbacks = {
      ...composer.callbacks,
      restoreRoomMute: meeting.callbacks.restoreRoomMute,
    };
    composer.priorRoomMute = await meeting.callbacks.parkRoomMute?.();
  }

  private async close(active: ActiveFocus, reason: AudioFocusTerminalReason): Promise<void> {
    if (!active.closePromise) {
      active.closePromise = (async () => {
        try {
          await active.callbacks.forceClose?.(reason);
        } finally {
          if (active.priorRoomMute !== undefined) {
            await active.callbacks.restoreRoomMute?.(active.priorRoomMute);
          }
        }
      })();
    }
    try {
      await active.closePromise;
    } finally {
      if (this.active === active) {
        this.active = null;
      }
    }
  }
}
