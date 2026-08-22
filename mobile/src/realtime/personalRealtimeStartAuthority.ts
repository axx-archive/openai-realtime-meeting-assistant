export type PersonalRealtimeGuardedStageResult<T> =
  | { current: true; value: T }
  | { current: false };

export type PersonalRealtimeStartAuthoritySnapshot = {
  sessionToken: string;
  authStorageGeneration: number;
  connectionGeneration: number;
  qualificationEpoch: number;
};

export type PersonalRealtimeStartAuthorityLive = {
  mounted: boolean;
  liveSessionToken: string | null;
  qualifiedAuthorityToken: string;
  authStorageGeneration: number;
  connectionGeneration: number;
  qualificationEpoch: number;
};

/**
 * One exact startup authority. Qualification is deliberately its own epoch:
 * the signed-in token can remain unchanged while the server revokes the
 * provider route. A false `/client-config` result advances that live epoch, so
 * every pending control/focus/capture/native-audio continuation fails its next
 * preflight even before React commits the hidden/teardown state.
 */
export function personalRealtimeStartAuthorityIsCurrent(
  snapshot: PersonalRealtimeStartAuthoritySnapshot,
  live: PersonalRealtimeStartAuthorityLive,
): boolean {
  return live.mounted
    && live.liveSessionToken === snapshot.sessionToken
    && live.qualifiedAuthorityToken === snapshot.sessionToken
    && live.authStorageGeneration === snapshot.authStorageGeneration
    && live.connectionGeneration === snapshot.connectionGeneration
    && live.qualificationEpoch === snapshot.qualificationEpoch;
}

/**
 * Admit one asynchronous startup stage only while the exact auth/transport
 * generation remains current. The preflight check prevents a late callback
 * from starting the next sensitive operation; the postflight check gives a
 * stage that already produced a resource one exact retirement hook.
 */
export async function runPersonalRealtimeGuardedStage<T>(options: {
  isCurrent(): boolean;
  run(): Promise<T>;
  retireStale?: (value: T) => void | Promise<void>;
}): Promise<PersonalRealtimeGuardedStageResult<T>> {
  if (!options.isCurrent()) return { current: false };
  const value = await options.run();
  if (options.isCurrent()) return { current: true, value };
  await options.retireStale?.(value);
  return { current: false };
}
