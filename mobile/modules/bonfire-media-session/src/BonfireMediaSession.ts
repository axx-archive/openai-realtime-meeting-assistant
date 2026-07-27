export interface MeetingAudioRouteSnapshot {
  category: string;
  mode: string;
  outputs: Array<{ name: string; type: string }>;
}

export interface NativeMediaSessionModule {
  activateVideoMeeting(): Promise<MeetingAudioRouteSnapshot>;
  deactivateVideoMeeting(): Promise<boolean>;
}

export interface MediaSessionClient {
  activateVideoMeeting(): Promise<MeetingAudioRouteSnapshot | null>;
  deactivateVideoMeeting(): Promise<boolean>;
}

export function createMediaSessionClient(
  nativeModule: NativeMediaSessionModule | null | undefined,
): MediaSessionClient {
  return {
    async activateVideoMeeting() {
      if (!nativeModule) return null;
      try {
        return await nativeModule.activateVideoMeeting();
      } catch {
        return null;
      }
    },
    async deactivateVideoMeeting() {
      if (!nativeModule) return false;
      try {
        return await nativeModule.deactivateVideoMeeting();
      } catch {
        return false;
      }
    },
  };
}
