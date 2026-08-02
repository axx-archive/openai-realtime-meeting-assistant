export interface MeetingAudioRouteSnapshot {
  generation: number;
  category: string;
  mode: string;
  outputs: Array<{ name: string; type: string }>;
}

export interface NativeMediaSessionModule {
  activateVideoMeeting(generation: number): Promise<MeetingAudioRouteSnapshot>;
  deactivateVideoMeeting(generation: number): Promise<boolean>;
}

export interface MediaSessionClient {
  activateVideoMeeting(generation: number): Promise<MeetingAudioRouteSnapshot | null>;
  deactivateVideoMeeting(generation: number): Promise<boolean>;
}

// Date-based generations remain monotonic across a JS fast refresh while still
// staying below Number.MAX_SAFE_INTEGER. Native code uses the same value as an
// authority fence, so a late operation from an earlier audio owner cannot
// reactivate or deactivate the replacement owner.
let lastIssuedMediaSessionGeneration = Date.now() * 1_000;

export function nextMediaSessionGeneration(now = Date.now()): number {
  const timestampFloor = Math.trunc(now) * 1_000;
  lastIssuedMediaSessionGeneration = Math.max(
    lastIssuedMediaSessionGeneration + 1,
    timestampFloor,
  );
  return lastIssuedMediaSessionGeneration;
}

function validGeneration(generation: number): boolean {
  return Number.isSafeInteger(generation) && generation > 0;
}

function validRouteSnapshot(
  value: unknown,
  generation: number,
): value is MeetingAudioRouteSnapshot {
  if (!value || typeof value !== 'object') return false;
  const snapshot = value as Partial<MeetingAudioRouteSnapshot>;
  return snapshot.generation === generation
    && typeof snapshot.category === 'string'
    && snapshot.category.trim().length > 0
    && typeof snapshot.mode === 'string'
    && snapshot.mode.trim().length > 0
    && Array.isArray(snapshot.outputs)
    && snapshot.outputs.length > 0
    && snapshot.outputs.every((output) => (
      Boolean(output)
      && typeof output.name === 'string'
      && output.name.trim().length > 0
      && typeof output.type === 'string'
      && output.type.trim().length > 0
    ));
}

export function createMediaSessionClient(
  nativeModule: NativeMediaSessionModule | null | undefined,
): MediaSessionClient {
  return {
    async activateVideoMeeting(generation) {
      if (!nativeModule || !validGeneration(generation)) return null;
      try {
        const snapshot = await nativeModule.activateVideoMeeting(generation);
        return validRouteSnapshot(snapshot, generation) ? snapshot : null;
      } catch {
        return null;
      }
    },
    async deactivateVideoMeeting(generation) {
      if (!nativeModule || !validGeneration(generation)) return false;
      try {
        return await nativeModule.deactivateVideoMeeting(generation);
      } catch {
        return false;
      }
    },
  };
}
