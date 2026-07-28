import type { ExpoConfig, ConfigContext } from 'expo/config';

// EAS project under paid org axxonlabs (not free-tier axx_archive).
const projectId =
  process.env.EAS_PROJECT_ID ||
  process.env.EXPO_PUBLIC_EAS_PROJECT_ID ||
  '30cd10a4-275d-45e3-8084-a1d7617b42f8';

const iosBundleIdentifier = 'xyz.thebonfire.app';
const iosAppGroupIdentifier = 'group.xyz.thebonfire.app';
const iosBroadcastExtensionBundleIdentifier =
  'xyz.thebonfire.app.broadcast';
const iosBroadcastExtensionTargetName = 'BonfireOSBroadcastExtension';

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
    owner: 'axxonlabs',
    ios: {
      supportsTablet: true,
      bundleIdentifier: iosBundleIdentifier,
      icon: {
        light: './assets/icon.png',
        dark: './assets/ios-icon-dark.png',
        tinted: './assets/ios-icon-tinted.png',
      },
      // Build 18 ships explicit portrait normalization for the adaptive camera,
      // safe stalled-camera recapture, and the quiet-join microphone hotfix.
      // Pin the multi-target release so the app and ReplayKit extension match.
      buildNumber: '18',
      // Public team identifier only; EAS continues to own the signing
      // certificates and provisioning profiles.
      appleTeamId: '73PT36P58W',
      associatedDomains: [
        'webcredentials:thebonfire.xyz',
        'applinks:thebonfire.xyz',
      ],
      entitlements: {
        'com.apple.security.application-groups': [iosAppGroupIdentifier],
      },
      infoPlist: {
        NSCameraUsageDescription:
          'BonfireOS uses the camera when you join a live room with video.',
        NSMicrophoneUsageDescription:
          'BonfireOS uses the microphone when you join a live room, talk to Scout, or dictate a message.',
        NSPhotoLibraryUsageDescription:
          'BonfireOS can attach photos to Scout threads and board cards.',
        // Audio keeps the call audible; VoIP lets iOS 18+ grant WebRTC's
        // multitasking camera access while the call is in Picture in Picture.
        UIBackgroundModes: ['audio', 'voip'],
        // react-native-webrtc uses these values to connect the main app's
        // screen capturer to the ReplayKit upload extension over rtc_SSFD.
        RTCAppGroupIdentifier: iosAppGroupIdentifier,
        RTCScreenSharingExtension: iosBroadcastExtensionBundleIdentifier,
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
        backgroundColor: '#0E0E10',
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
      'expo-asset',
      'expo-image',
      // Dictation records locally and transcribes server-side with the company
      // vocabulary lane. The system mic prompt says nothing about where audio
      // goes, so the app also shows a first-use disclosure (design §12.5).
      [
        'expo-audio',
        {
          microphonePermission:
            'BonfireOS uses the microphone to dictate messages and talk to Scout.',
        },
      ],
      // Native push. The plugin configures the APNs entitlement (development;
      // Xcode promotes it for release builds). Adding this changes native
      // config, so it requires a rebuild — and CocoaPods on this Mac needs
      // LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 or pod install throws
      // "Unicode Normalization not appropriate for ASCII-8BIT".
      'expo-notifications',
      './plugins/withWebRTCMultitaskingCamera',
      [
        './plugins/withWebRTCBroadcastExtension',
        { stage: 'native' },
      ],
      '@bacons/apple-targets',
      [
        './plugins/withWebRTCBroadcastExtension',
        { stage: 'eas' },
      ],
      // Must run after the WebRTC plugins so it edits the final Podfile.
      './plugins/withSwiftUICoreWeakLink',
      'expo-secure-store',
      'expo-font',
      'expo-sharing',
      [
        'expo-build-properties',
        {
          ios: {
            deploymentTarget: '16.4',
          },
        },
      ],
      [
        'expo-splash-screen',
        {
          // Live PWA theme_color / paper-50
          backgroundColor: '#F5F5F7',
          image: './assets/splash-icon.png',
          imageWidth: 144,
          resizeMode: 'contain',
          dark: {
            backgroundColor: '#0E0E10',
            image: './assets/splash-icon-dark.png',
          },
        },
      ],
    ],
    extra: {
      apiBaseUrl: process.env.EXPO_PUBLIC_API_BASE_URL ?? 'https://thebonfire.xyz',
      webAppUrl: process.env.EXPO_PUBLIC_WEB_APP_URL ?? 'https://thebonfire.xyz',
      eas: projectId
        ? {
            projectId,
            build: {
              experimental: {
                ios: {
                  appExtensions: [
                    {
                      targetName: iosBroadcastExtensionTargetName,
                      bundleIdentifier:
                        iosBroadcastExtensionBundleIdentifier,
                      parentBundleIdentifier: iosBundleIdentifier,
                      entitlements: {
                        'com.apple.security.application-groups': [
                          iosAppGroupIdentifier,
                        ],
                      },
                    },
                  ],
                },
              },
            },
          }
        : undefined,
    },
  };

  return expo;
};
