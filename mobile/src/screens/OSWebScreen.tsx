import React, { useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { WebView } from 'react-native-webview';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { WEB_APP_URL } from '../config';
import type { RootStackParamList } from '../navigation/types';
import { colors } from '../theme/colors';
import { SafeAreaView } from 'react-native-safe-area-context';

type Props = NativeStackScreenProps<RootStackParamList, 'OSWeb'>;

/**
 * Full web OS surface. Injects the native session as the bonfire_session
 * cookie so the SPA is already signed in — same session as API tabs.
 */
export function OSWebScreen({ route, navigation }: Props) {
  const { sessionToken, user } = useAuth();
  const path = route.params?.path ?? '/';
  const title = route.params?.title ?? 'BonfireOS';
  const webRef = useRef<WebView>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const uri = useMemo(() => {
    const base = WEB_APP_URL.replace(/\/$/, '');
    if (path.startsWith('http')) return path;
    return `${base}${path.startsWith('/') ? path : `/${path}`}`;
  }, [path]);

  const injectedBefore = useMemo(() => {
    if (!sessionToken) return undefined;
    // Set the HttpOnly-equivalent session cookie for this origin before the
    // SPA boots /auth/me. document.cookie cannot set HttpOnly; the server
    // still accepts the same value via Cookie header from the WebView store.
    const secure = WEB_APP_URL.startsWith('https') ? '; Secure' : '';
    const script = `
      (function() {
        try {
          document.cookie = "bonfire_session=${sessionToken}; Path=/${secure}; SameSite=Lax; Max-Age=${30 * 24 * 3600}";
        } catch (e) {}
        true;
      })();
    `;
    return script;
  }, [sessionToken]);

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      <View style={styles.toolbar}>
        <Pressable onPress={() => navigation.goBack()} hitSlop={12}>
          <Text style={styles.back}>Close</Text>
        </Pressable>
        <Text style={styles.title} numberOfLines={1}>
          {title}
        </Text>
        <Text style={styles.user} numberOfLines={1}>
          {user?.name ?? ''}
        </Text>
      </View>

      {error ? (
        <View style={styles.errorBox}>
          <Text style={styles.errorText}>{error}</Text>
          <Pressable onPress={() => { setError(null); webRef.current?.reload(); }}>
            <Text style={styles.retry}>Reload</Text>
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
          source={{ uri }}
          style={styles.web}
          sharedCookiesEnabled
          thirdPartyCookiesEnabled
          injectedJavaScriptBeforeContentLoaded={injectedBefore}
          onLoadStart={() => setLoading(true)}
          onLoadEnd={() => setLoading(false)}
          onError={() => {
            setLoading(false);
            setError('Could not load the OS. Check your connection.');
          }}
          onHttpError={(e) => {
            if (e.nativeEvent.statusCode >= 500) {
              setError(`Server error ${e.nativeEvent.statusCode}`);
            }
          }}
          allowsBackForwardNavigationGestures
          setSupportMultipleWindows={false}
          applicationNameForUserAgent="BonfireOS-Expo"
        />
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  toolbar: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 16,
    paddingVertical: 10,
    gap: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.border,
    backgroundColor: colors.bgElevated,
  },
  back: {
    color: colors.ember,
    fontWeight: '600',
    fontSize: 16,
    width: 56,
  },
  title: {
    flex: 1,
    textAlign: 'center',
    fontSize: 16,
    fontWeight: '600',
    color: colors.text,
  },
  user: {
    width: 56,
    textAlign: 'right',
    fontSize: 13,
    color: colors.textTertiary,
  },
  webWrap: {
    flex: 1,
  },
  web: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  loadingOverlay: {
    ...StyleSheet.absoluteFill,
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 2,
    backgroundColor: 'rgba(247,246,243,0.7)',
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
    fontSize: 13,
  },
  retry: {
    color: colors.accent,
    fontWeight: '600',
  },
});
