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
    name: 'Stride',
    description:
      'The voice-first company OS where conversation becomes memory, approved work, and verified results.',
    slug: 'bonfireos',
    version: '1.0.0',
    // iPhone and iPad work threads are adaptive. Keep this aligned with the
    // checked-in plist so Expo regeneration cannot silently remove landscape
    // or iPad Split View support.
    orientation: 'default',
    icon: './assets/icon.png',
    userInterfaceStyle: 'automatic',
    // Retain the installed deep-link scheme while adding the visible Stride
    // identity. Existing password-reset and room links must keep opening.
    scheme: ['stride', 'bonfireos'],
    owner: 'axxonlabs',
    ios: {
      supportsTablet: true,
      bundleIdentifier: iosBundleIdentifier,
      // The Icon Composer bundle, not the PNG appearance set. iOS composes the
      // specular, the shadow, the dark ground, and the Tinted and Clear
      // renditions from flat layers, so the icon is rendered by the OS instead
      // of being a picture of what we guessed the OS would do — and Clear
      // Light/Dark exist at all, which the PNG path cannot reach.
      //
      // The PNGs are still generated (Android, web manifest, Xcode catalog)
      // and remain the fallback if this bundle is ever rejected.
      // Regenerate both with `npm run brand:regen` from the repo root.
      icon: './assets/Stride.icon',
      // Build 63 carries the Priority-1 meeting-intelligence, private Realtime
      // recovery, current AppIcon, permanent Meeting Record, workstream-owned
      // Project correction, and Board-retirement candidate. It retains Build
      // 62's exact Project attachment/reply source-group compatibility.
      // Pin the app and ReplayKit extension to the same release.
      buildNumber: '63',
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
          'Stride uses the camera when you join a live room with video.',
        NSMicrophoneUsageDescription:
          'Stride uses the microphone when you join a live room, talk to Scout, or dictate a message.',
        NSPhotoLibraryUsageDescription:
          'Stride uses selected photos for your profile and chat attachments.',
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
        // Matches android-icon-background.png. Launchers that use the colour
        // instead of the image must land on the same ground, not on ink.
        backgroundColor: '#CFC5B7',
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
            'Stride uses the microphone to dictate messages and talk to Scout.',
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
          // The OS splash is the cradle at rest. The in-app launch overlay uses
          // the same component and scale, then fades into the live Canvas.
          backgroundColor: '#CFC5B7',
          image: './assets/splash-icon.png',
          // Matches the live Canvas cradle's 391 * 0.8 rendered width so the
          // native launch image hands off without changing scale.
          imageWidth: 313,
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
