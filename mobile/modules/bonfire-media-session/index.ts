import { requireOptionalNativeModule } from 'expo';
import { Platform } from 'react-native';

import {
  createMediaSessionClient,
  type NativeMediaSessionModule,
} from './src/BonfireMediaSession';

const nativeModule = Platform.OS === 'ios'
  ? requireOptionalNativeModule<NativeMediaSessionModule>('BonfireMediaSession')
  : undefined;

const BonfireMediaSession = createMediaSessionClient(nativeModule);

export default BonfireMediaSession;
export { createMediaSessionClient };
export type {
  MediaSessionClient,
  MeetingAudioRouteSnapshot,
  NativeMediaSessionModule,
} from './src/BonfireMediaSession';
