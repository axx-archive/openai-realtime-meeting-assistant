import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Linking,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { WebView } from 'react-native-webview';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useAuth } from '../auth/AuthContext';
import { WEB_APP_URL } from '../config';
import type { RootStackParamList } from '../navigation/types';
import { colors, product, type } from '../theme/tokens';
import {
  classifyOSWebNavigation,
  isStudioDownloadBridgeMessage,
  parseStudioDownloadMessage,
  parseStudioFileDownloadUrl,
  type StudioDownloadRequest,
} from '../artifacts/studioDownloadProtocol';
import { shareStudioDownload } from '../artifacts/studioDownloads';

type Props = NativeStackScreenProps<RootStackParamList, 'OSWeb'>;

type StudioCloseMessage = {
  type: 'stride.studio.close';
  version: 1;
  kind: 'deck' | 'document';
  mode: 'edit' | 'present' | 'view';
  artifactId: string;
};

const STUDIO_ARTIFACT_ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,159}$/;

function studioCloseMessageMatchesPath(raw: string, currentPath: string): boolean {
  if (!raw || raw.length > 512 || !currentPath.startsWith('/') || currentPath.startsWith('//')) return false;
  let message: StudioCloseMessage;
  let destination: URL;
  try {
    const candidate = JSON.parse(raw) as Partial<StudioCloseMessage> & Record<string, unknown>;
    const keys = Object.keys(candidate).sort().join(',');
    if (keys !== 'artifactId,kind,mode,type,version') return false;
    if (candidate.type !== 'stride.studio.close' || candidate.version !== 1) return false;
    if (candidate.kind !== 'deck' && candidate.kind !== 'document') return false;
    if (candidate.mode !== 'edit' && candidate.mode !== 'present' && candidate.mode !== 'view') return false;
    if (candidate.kind === 'document' && candidate.mode !== 'edit' && candidate.mode !== 'view') return false;
    if (candidate.kind === 'deck' && candidate.mode === 'view') return false;
    if (typeof candidate.artifactId !== 'string' || !STUDIO_ARTIFACT_ID.test(candidate.artifactId)) return false;
    message = candidate as StudioCloseMessage;
    destination = new URL(currentPath, 'https://stride.invalid');
  } catch {
    return false;
  }

  const route = destination.pathname.match(/^\/studio\/(deck|document)\/([^/]+)$/);
  if (!route) return false;
  let routeArtifactId = '';
  try {
    routeArtifactId = decodeURIComponent(route[2]);
  } catch {
    return false;
  }
  return route[1] === message.kind
    && routeArtifactId === message.artifactId
    && destination.searchParams.get('mode') === message.mode;
}

/**
 * Full live web OS. Injects the native session cookie so the SPA is signed in
 * with the same Glass & Ink UI the production site serves — no forked design.
 */
export function OSWebScreen({ route, navigation }: Props) {
  const { sessionToken, user } = useAuth();
  const path = route.params?.path ?? '/';
  const title = route.params?.title ?? product.name;
  const webRef = useRef<WebView>(null);
  const dismissedByWebRef = useRef(false);
  const downloadInFlightRef = useRef(false);
  const mountedRef = useRef(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [downloadStatus, setDownloadStatus] = useState<string | null>(null);
  const [downloadError, setDownloadError] = useState<string | null>(null);

  const uri = useMemo(() => {
    const base = WEB_APP_URL.replace(/\/$/, '');
    const safePath = path.startsWith('/') && !path.startsWith('//') ? path : '/';
    return `${base}/auth/native-web-session?path=${encodeURIComponent(safePath)}`;
  }, [path]);

  const isStudioPath = path.startsWith('/studio/deck/') || path.startsWith('/studio/document/');

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const handleStudioDownload = useCallback(async (request: StudioDownloadRequest) => {
    if (downloadInFlightRef.current) {
      setDownloadError('Finish the current file action before starting another download.');
      return;
    }
    downloadInFlightRef.current = true;
    setDownloadError(null);
    setDownloadStatus(`Preparing ${request.format === 'pptx' ? 'PowerPoint' : 'PDF'}…`);
    try {
      await shareStudioDownload(sessionToken ?? '', request);
    } catch (caught) {
      if (mountedRef.current) {
        setDownloadError(caught instanceof Error ? caught.message : 'The file could not be downloaded.');
      }
    } finally {
      downloadInFlightRef.current = false;
      if (mountedRef.current) setDownloadStatus(null);
    }
  }, [sessionToken]);

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      <View style={styles.toolbar}>
        <Pressable
          accessibilityRole="button"
          onPress={() => navigation.goBack()} hitSlop={12} style={styles.side}>
          <Text style={styles.back}>Close</Text>
        </Pressable>
        <Text style={styles.title} numberOfLines={1}>
          {title}
        </Text>
        <Text style={[styles.side, styles.user]} numberOfLines={1}>
          {user?.name ?? ''}
        </Text>
      </View>

      {error ? (
        <View style={styles.errorBox}>
          <Text style={styles.errorText}>{error}</Text>
          <Pressable
            accessibilityRole="button"
            onPress={() => {
              setError(null);
              webRef.current?.reload();
            }}
          >
            <Text style={styles.retry}>Reload</Text>
          </Pressable>
        </View>
      ) : null}

      {downloadStatus ? (
        <View style={styles.downloadNotice} accessibilityRole="progressbar" accessibilityLiveRegion="polite">
          <ActivityIndicator size="small" color={colors.accent} />
          <Text style={styles.downloadNoticeText}>{downloadStatus}</Text>
        </View>
      ) : null}

      {downloadError ? (
        <View style={styles.errorBox} accessibilityLiveRegion="assertive">
          <Text style={styles.errorText}>{downloadError}</Text>
          <Pressable accessibilityRole="button" onPress={() => setDownloadError(null)}>
            <Text style={styles.retry}>Dismiss</Text>
          </Pressable>
        </View>
      ) : null}

      <View style={styles.webWrap}>
        {loading ? (
          <View style={styles.loadingOverlay}>
            <ActivityIndicator color={colors.accent} />
          </View>
        ) : null}
        <WebView
          ref={webRef}
          style={styles.web}
          sharedCookiesEnabled
          source={{
            uri,
            headers: sessionToken
              ? {
                  Authorization: `Bearer ${sessionToken}`,
                  'X-Bonfire-Client': 'expo',
                }
              : undefined,
          }}
          onShouldStartLoadWithRequest={(request) => {
            const decision = classifyOSWebNavigation(request.url, WEB_APP_URL);
            if (decision.action === 'allow') return true;
            if (decision.action === 'external') {
              void Linking.openURL(decision.url).catch(() => {
                if (mountedRef.current) setDownloadError('That link could not be opened safely.');
              });
            }
            return false;
          }}
          onMessage={({ nativeEvent }) => {
            const download = parseStudioDownloadMessage(nativeEvent.data, path, WEB_APP_URL);
            if (download) {
              void handleStudioDownload(download);
              return;
            }
            if (isStudioDownloadBridgeMessage(nativeEvent.data)) {
              setDownloadError('Stride blocked an invalid Studio download request.');
              return;
            }
            if (dismissedByWebRef.current || !studioCloseMessageMatchesPath(nativeEvent.data, path)) return;
            dismissedByWebRef.current = true;
            navigation.goBack();
          }}
          onFileDownload={isStudioPath ? ({ nativeEvent }) => {
            const download = parseStudioFileDownloadUrl(nativeEvent.downloadUrl, path, WEB_APP_URL);
            if (!download) {
              setDownloadError('Stride blocked an unsafe or unsupported download.');
              return;
            }
            void handleStudioDownload(download);
          } : undefined}
          onLoadStart={() => setLoading(true)}
          onLoadEnd={() => setLoading(false)}
          onError={() => {
            setLoading(false);
            setError('Could not load Stride. Check your connection.');
          }}
          onHttpError={(e) => {
            if (e.nativeEvent.statusCode >= 500) {
              setError(`Server error ${e.nativeEvent.statusCode}`);
            }
          }}
          allowsBackForwardNavigationGestures
          setSupportMultipleWindows={false}
          applicationNameForUserAgent="Stride-Expo"
        />
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  toolbar: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 16,
    paddingVertical: 10,
    gap: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line1,
    backgroundColor: colors.surface1,
  },
  side: {
    width: 64,
  },
  back: {
    ...type.headline,
    color: colors.text2,
    fontSize: 15,
  },
  title: {
    flex: 1,
    textAlign: 'center',
    ...type.headline,
    color: colors.text1,
  },
  user: {
    textAlign: 'right',
    ...type.caption,
    color: colors.text3,
  },
  webWrap: {
    flex: 1,
  },
  web: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  loadingOverlay: {
    ...StyleSheet.absoluteFill,
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 2,
    backgroundColor: 'rgba(245,245,247,0.72)',
  },
  downloadNotice: {
    minHeight: 44,
    paddingHorizontal: 16,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 10,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line1,
    backgroundColor: colors.surface2,
  },
  downloadNoticeText: {
    ...type.caption,
    color: colors.text2,
    fontVariant: ['tabular-nums'],
  },
  errorBox: {
    padding: 12,
    backgroundColor: colors.dangerSoft,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
  },
  errorText: {
    color: colors.danger,
    flex: 1,
    marginRight: 12,
    ...type.caption,
  },
  retry: {
    color: colors.accent,
    fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600',
  },
});
