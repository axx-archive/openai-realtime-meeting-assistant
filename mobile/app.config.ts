import type { ExpoConfig, ConfigContext } from 'expo/config';

const projectId =
  process.env.EAS_PROJECT_ID ||
  process.env.EXPO_PUBLIC_EAS_PROJECT_ID ||
  '0236310a-4904-4eaf-b1d4-a86e50e49d88';

export default ({ config }: ConfigContext): ExpoConfig => {
  const expo: ExpoConfig = {
    ...config,
    name: 'BonfireOS',
    slug: 'bonfireos',
    version: '1.0.0',
    orientation: 'portrait',
    icon: './assets/icon.png',
    userInterfaceStyle: 'automatic',
    scheme: 'bonfireos',
    ios: {
      supportsTablet: true,
      bundleIdentifier: 'xyz.thebonfire.app',
      buildNumber: '1',
      infoPlist: {
        NSCameraUsageDescription:
          'BonfireOS uses the camera when you join a live room with video.',
        NSMicrophoneUsageDescription:
          'BonfireOS uses the microphone when you join a live room or talk to Scout.',
        NSPhotoLibraryUsageDescription:
          'BonfireOS can attach photos to Scout threads and board cards.',
        UIBackgroundModes: ['audio', 'voip'],
      },
      config: {
        usesNonExemptEncryption: false,
      },
    },
    android: {
      package: 'xyz.thebonfire.app',
      adaptiveIcon: {
        foregroundImage: './assets/android-icon-foreground.png',
        backgroundImage: './assets/android-icon-background.png',
        monochromeImage: './assets/android-icon-monochrome.png',
        backgroundColor: '#F7F6F3',
      },
      permissions: [
        'CAMERA',
        'RECORD_AUDIO',
        'MODIFY_AUDIO_SETTINGS',
        'INTERNET',
      ],
    },
    web: {
      favicon: './assets/favicon.png',
    },
    plugins: [
      'expo-secure-store',
      [
        'expo-splash-screen',
        {
          backgroundColor: '#F7F6F3',
          image: './assets/splash-icon.png',
        },
      ],
    ],
    extra: {
      apiBaseUrl: process.env.EXPO_PUBLIC_API_BASE_URL ?? 'https://thebonfire.xyz',
      webAppUrl: process.env.EXPO_PUBLIC_WEB_APP_URL ?? 'https://thebonfire.xyz',
      eas: projectId ? { projectId } : undefined,
    },
    owner: 'axx_archive',
  };

  return expo;
};
