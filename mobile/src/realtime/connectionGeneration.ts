export type SocketGeneration = Readonly<{
  id: number;
  kind: 'socket';
}>;

export type PeerGeneration = Readonly<{
  id: number;
  kind: 'peer';
  socket: SocketGeneration;
}>;

export type JoinAttemptGeneration = Readonly<{
  id: number;
  kind: 'join-attempt';
}>;

export type NativeRoomConnectionGenerationGuard = {
  activateSocket: () => SocketGeneration;
  retireSocket: (generation?: SocketGeneration | null) => void;
  activatePeer: (socket: SocketGeneration) => PeerGeneration | null;
  retirePeer: (generation?: PeerGeneration | null) => void;
  isCurrentSocket: (generation: SocketGeneration | null | undefined) => boolean;
  isCurrentPeer: (generation: PeerGeneration | null | undefined) => boolean;
};

export type NativeRoomJoinAttemptGuard = {
  begin: () => JoinAttemptGeneration;
  cancel: (generation?: JoinAttemptGeneration | null) => void;
  isCurrent: (generation: JoinAttemptGeneration | null | undefined) => boolean;
};

export type GenerationSettlement<T> =
  | { current: true; value: T }
  | { current: false };

/** Waits for native async work, then treats both resolution and rejection from a retired generation as stale. */
export async function settleGenerationOperation<T>(
  operation: Promise<T>,
  isCurrent: () => boolean,
): Promise<GenerationSettlement<T>> {
  try {
    const value = await operation;
    return isCurrent() ? { current: true, value } : { current: false };
  } catch (error) {
    if (!isCurrent()) return { current: false };
    throw error;
  }
}

/** Release a resource returned after its owning async generation was cancelled or replaced. */
export async function settleGenerationResource<T>(
  operation: Promise<T>,
  isCurrent: () => boolean,
  release: (value: T) => void,
): Promise<GenerationSettlement<T>> {
  try {
    const value = await operation;
    if (isCurrent()) return { current: true, value };
    release(value);
    return { current: false };
  } catch (error) {
    if (!isCurrent()) return { current: false };
    throw error;
  }
}

/** Owns permission/config work that begins before a signaling socket exists. */
export function createNativeRoomJoinAttemptGuard(): NativeRoomJoinAttemptGuard {
  let nextId = 0;
  let active: JoinAttemptGeneration | null = null;
  return {
    begin: () => {
      const generation: JoinAttemptGeneration = Object.freeze({
        id: ++nextId,
        kind: 'join-attempt',
      });
      active = generation;
      return generation;
    },
    cancel: (generation) => {
      if (generation && active !== generation) return;
      active = null;
    },
    isCurrent: (generation) => Boolean(generation) && active === generation,
  };
}

/**
 * Owns the signaling/media generation independently from the native objects.
 * React Native WebRTC may deliver callbacks after close(), and awaited WebRTC
 * operations may settle after a replacement connection has already joined.
 */
export function createNativeRoomConnectionGenerationGuard(): NativeRoomConnectionGenerationGuard {
  let nextId = 0;
  let activeSocket: SocketGeneration | null = null;
  let activePeer: PeerGeneration | null = null;

  const isCurrentSocket = (generation: SocketGeneration | null | undefined): boolean => (
    Boolean(generation) && activeSocket === generation
  );
  const isCurrentPeer = (generation: PeerGeneration | null | undefined): boolean => (
    Boolean(generation)
    && activePeer === generation
    && activeSocket === generation?.socket
  );

  return {
    activateSocket: () => {
      const generation: SocketGeneration = Object.freeze({
        id: ++nextId,
        kind: 'socket',
      });
      activeSocket = generation;
      activePeer = null;
      return generation;
    },
    retireSocket: (generation) => {
      if (generation && activeSocket !== generation) return;
      activeSocket = null;
      activePeer = null;
    },
    activatePeer: (socket) => {
      if (activeSocket !== socket) return null;
      const generation: PeerGeneration = Object.freeze({
        id: ++nextId,
        kind: 'peer',
        socket,
      });
      activePeer = generation;
      return generation;
    },
    retirePeer: (generation) => {
      if (generation && activePeer !== generation) return;
      activePeer = null;
    },
    isCurrentSocket,
    isCurrentPeer,
  };
}
