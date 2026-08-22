import { api } from '../api/client';
import { NativeClientConfigCache } from './personalRealtimeQualification';

/**
 * One authenticated `/client-config` transport shared by Room and personal
 * Realtime. The pure cache owns coalescing and account fencing; this module is
 * the only native-API adapter so pure lifecycle/authority tests stay portable.
 */
export const nativeClientConfigCache = new NativeClientConfigCache(
  (sessionToken) => api.clientConfig(sessionToken),
);
