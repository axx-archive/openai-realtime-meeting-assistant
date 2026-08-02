import React, { useMemo, useRef, useState } from 'react';
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

type Props = NativeStackScreenProps<RootStackParamList, 'OSWeb'>;

/**
 * Full live web OS. Injects the native session cookie so the SPA is signed in
 * with the same Glass & Ink UI the production site serves — no forked design.
 */
export function OSWebScreen({ route, navigation }: Props) {
  const { sessionToken, user } = useAuth();
  const path = route.params?.path ?? '/';
  const title = route.params?.title ?? product.name;
  const webRef = useRef<WebView>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const uri = useMemo(() => {
    const base = WEB_APP_URL.replace(/\/$/, '');
    const safePath = path.startsWith('/') && !path.startsWith('//') ? path : '/';
    return `${base}/auth/native-web-session?path=${encodeURIComponent(safePath)}`;
  }, [path]);

  const allowedHost = useMemo(() => {
    try {
      return new URL(WEB_APP_URL).host;
    } catch {
      return '';
    }
  }, []);

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
            try {
              const next = new URL(request.url);
              if (next.host === allowedHost) return true;
              void Linking.openURL(request.url);
              return false;
            } catch {
              return false;
            }
          }}
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
