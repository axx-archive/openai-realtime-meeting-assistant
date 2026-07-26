import { requireOptionalNativeModule } from 'expo';
import { Platform } from 'react-native';

import {
  createCameraFramingClient,
  type CameraFramingCapabilities,
  type CameraFramingClient,
  type CameraFramingOperationResult,
  type CameraFramingReasonCode,
  type NativeCameraFramingModule,
} from './src/BonfireCameraFraming';

const nativeModule =
  Platform.OS === 'ios'
    ? requireOptionalNativeModule<NativeCameraFramingModule>('BonfireCameraFraming')
    : undefined;

const BonfireCameraFraming = createCameraFramingClient(nativeModule, Platform.OS);

export default BonfireCameraFraming;
export { createCameraFramingClient };
export type {
  CameraFramingCapabilities,
  CameraFramingClient,
  CameraFramingOperationResult,
  CameraFramingReasonCode,
  NativeCameraFramingModule,
};
