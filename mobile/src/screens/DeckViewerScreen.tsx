import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Linking,
  Pressable,
  StyleSheet,
  Text,
  View,
  useWindowDimensions,
} from 'react-native';
import { SafeAreaView, useSafeAreaInsets } from 'react-native-safe-area-context';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { SymbolView } from 'expo-symbols';
import { WebView } from 'react-native-webview';

import { api } from '../api/client';
import { buildApiUrl } from '../api/requestHelpers';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL } from '../config';
import {
  DECK_PREVIEW_NAVIGATION_JS,
  deckPreviewNavigationCommand,
  deckPreviewNavigationTarget,
  initialDeckPreviewNavigationState,
  parseDeckPreviewNavigationMessage,
  type DeckPreviewNavigationState,
} from '../messaging/deckPreviewNavigation';
import type { RootStackParamList } from '../navigation/types';
import { Glass } from '../theme/glass';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { nativeDeckFrame, nativeDeckRenderPath } from '../artifacts/nativeDeckViewer';

type Props = NativeStackScreenProps<RootStackParamList, 'DeckViewer'>;

/**
 * Native read-only presentation surface. Deck Studio editing deliberately does
 * not load in iOS: a render token can return only the admitted deck HTML, so a
 * malformed Studio/API response can never become a screenful of JSON.
 */
export function DeckViewerScreen({ route, navigation }: Props) {
  const { sessionToken } = useAuth();
  const { width, height } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const webViewRef = useRef<WebView>(null);
  const navigationRef = useRef<DeckPreviewNavigationState>(initialDeckPreviewNavigationState());
  const [deckUrl, setDeckUrl] = useState('');
  const [navigationState, setNavigationState] = useState<DeckPreviewNavigationState>(initialDeckPreviewNavigationState);
  const [error, setError] = useState('');
  const [retryNonce, setRetryNonce] = useState(0);
  const frame = useMemo(() => nativeDeckFrame(
    Math.max(0, width - insets.left - insets.right),
    Math.max(0, height - insets.top - insets.bottom),
  ), [height, insets.bottom, insets.left, insets.right, insets.top, width]);
  const title = String(route.params.title ?? 'Presentation').trim() || 'Presentation';

  const commitNavigation = useCallback((next: DeckPreviewNavigationState) => {
    navigationRef.current = next;
    setNavigationState(next);
  }, []);

  const resetNavigation = useCallback(() => {
    commitNavigation(initialDeckPreviewNavigationState());
  }, [commitNavigation]);

  useEffect(() => {
    let active = true;
    setDeckUrl('');
    setError('');
    resetNavigation();
    if (!sessionToken || !route.params.artifactId.trim()) {
      setError('This presentation is unavailable.');
      return () => { active = false; };
    }
    void api.artifactRenderToken(sessionToken, route.params.artifactId)
      .then((response) => {
        if (!active) return;
        const path = nativeDeckRenderPath(response.url);
        if (!path) {
          setError('This presentation is not ready to view.');
          return;
        }
        setDeckUrl(buildApiUrl(API_BASE_URL, path));
      })
      .catch(() => {
        if (active) setError('This presentation could not be opened.');
      });
    return () => { active = false; };
  }, [resetNavigation, retryNonce, route.params.artifactId, sessionToken]);

  const navigateSlide = useCallback((direction: 'previous' | 'next') => {
    const target = deckPreviewNavigationTarget(navigationRef.current, direction);
    if (target === null || !webViewRef.current) return;
    commitNavigation({ ...navigationRef.current, currentIndex: target });
    webViewRef.current.injectJavaScript(deckPreviewNavigationCommand(target));
  }, [commitNavigation]);

  const ready = navigationState.status === 'ready';

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
      <View style={styles.toolbar}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Close presentation"
          hitSlop={8}
          onPress={() => navigation.goBack()}
          style={({ pressed }) => [styles.toolbarButton, pressed && styles.pressed]}
        >
          <SymbolView name="xmark" size={16} tintColor="#FFFFFF" />
        </Pressable>
        <View style={styles.titleCopy}>
          <Text accessibilityRole="header" maxFontSizeMultiplier={1.6} numberOfLines={1} style={styles.title}>
            {title}
          </Text>
          <Text style={styles.subtitle}>PRESENTATION</Text>
        </View>
        {route.params.desktopEditable === true ? (
          <View accessibilityLabel="Editing is available on desktop" style={styles.desktopBadge}>
            <SymbolView name="desktopcomputer" size={13} tintColor="#FFFFFF" />
            {!frame.compact ? <Text style={styles.desktopBadgeText}>Edit on desktop</Text> : null}
          </View>
        ) : <View style={styles.toolbarSpacer} />}
      </View>

      <View style={styles.stage}>
        <View style={[styles.deckFrame, { width: frame.width, height: frame.height }]}>
          {deckUrl ? (
            <WebView
              ref={webViewRef}
              accessibilityLabel={`${title} slide canvas`}
              allowsBackForwardNavigationGestures={false}
              bounces={false}
              domStorageEnabled
              injectedJavaScript={DECK_PREVIEW_NAVIGATION_JS}
              javaScriptEnabled
              onContentProcessDidTerminate={() => {
                commitNavigation({ status: 'error', currentIndex: 0, slideCount: 0 });
                setError('The presentation viewer stopped.');
              }}
              onError={() => {
                commitNavigation({ status: 'error', currentIndex: 0, slideCount: 0 });
                setError('The presentation could not be rendered.');
              }}
              onHttpError={({ nativeEvent }) => {
                if (nativeEvent.statusCode < 400) return;
                commitNavigation({ status: 'error', currentIndex: 0, slideCount: 0 });
                setError('The presentation is no longer available.');
              }}
              onLoadStart={resetNavigation}
              onMessage={({ nativeEvent }) => {
                const next = parseDeckPreviewNavigationMessage(nativeEvent.data);
                if (!next) return;
                commitNavigation(next);
                if (next.status === 'error') setError('The presentation could not be rendered.');
              }}
              onShouldStartLoadWithRequest={(request) => {
                if (request.url === deckUrl || request.url === 'about:blank') return true;
                if (/^https?:\/\//u.test(request.url)) {
                  void Linking.openURL(request.url).catch(() => undefined);
                }
                return false;
              }}
              originWhitelist={['https://*', 'http://*']}
              scrollEnabled={false}
              setSupportMultipleWindows={false}
              showsHorizontalScrollIndicator={false}
              showsVerticalScrollIndicator={false}
              source={{ uri: deckUrl }}
              style={[styles.webView, !ready && styles.webViewHidden]}
            />
          ) : null}
          {!ready && !error ? (
            <View accessibilityLabel="Loading presentation" accessibilityRole="progressbar" style={styles.loading}>
              <ActivityIndicator color="#FFFFFF" size="small" />
              <Text style={styles.loadingText}>Preparing slides…</Text>
            </View>
          ) : null}
          {error ? (
            <View accessibilityRole="alert" style={styles.error}>
              <View style={styles.errorIcon}>
                <SymbolView name="exclamationmark.triangle.fill" size={19} tintColor={colors.ember} />
              </View>
              <Text style={styles.errorTitle}>Presentation unavailable</Text>
              <Text maxFontSizeMultiplier={1.8} style={styles.errorBody}>{error}</Text>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Retry presentation"
                onPress={() => setRetryNonce((current) => current + 1)}
                style={({ pressed }) => [styles.retry, pressed && styles.retryPressed]}
              >
                <Text style={styles.retryText}>Try again</Text>
              </Pressable>
            </View>
          ) : null}
        </View>
      </View>

      <View style={styles.controlsSlot}>
        {ready ? (
          <Glass accessibilityLabel="Presentation controls" radius={radius.full} variant="clear" style={styles.controls}>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Previous slide"
              accessibilityState={{ disabled: navigationState.currentIndex === 0 }}
              disabled={navigationState.currentIndex === 0}
              onPress={() => navigateSlide('previous')}
              style={({ pressed }) => [styles.controlButton, navigationState.currentIndex === 0 && styles.controlDisabled, pressed && styles.controlPressed]}
            >
              <SymbolView name="chevron.left" size={17} tintColor="#FFFFFF" />
            </Pressable>
            <Text
              accessibilityLiveRegion="polite"
              accessibilityLabel={`Slide ${navigationState.currentIndex + 1} of ${navigationState.slideCount}`}
              style={styles.slideCount}
            >
              {navigationState.currentIndex + 1} / {navigationState.slideCount}
            </Text>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Next slide"
              accessibilityState={{ disabled: navigationState.currentIndex >= navigationState.slideCount - 1 }}
              disabled={navigationState.currentIndex >= navigationState.slideCount - 1}
              onPress={() => navigateSlide('next')}
              style={({ pressed }) => [styles.controlButton, navigationState.currentIndex >= navigationState.slideCount - 1 && styles.controlDisabled, pressed && styles.controlPressed]}
            >
              <SymbolView name="chevron.right" size={17} tintColor="#FFFFFF" />
            </Pressable>
          </Glass>
        ) : null}
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: '#070708' },
  toolbar: { minHeight: 60, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[4] },
  toolbarButton: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: 'rgba(255,255,255,0.10)' },
  pressed: { opacity: 0.82, transform: [{ scale: 0.96 }] },
  titleCopy: { flex: 1, minWidth: 0, alignItems: 'center' },
  title: { ...type.headline, color: '#FFFFFF', textAlign: 'center' },
  subtitle: { ...type.label, marginTop: 1, color: 'rgba(255,255,255,0.52)', letterSpacing: 0.8 },
  desktopBadge: { minWidth: hitMin, minHeight: hitMin, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6, paddingHorizontal: space[3], borderRadius: radius.full, backgroundColor: 'rgba(255,255,255,0.10)' },
  desktopBadgeText: { ...type.captionMedium, color: '#FFFFFF' },
  toolbarSpacer: { width: hitMin, height: hitMin },
  stage: { flex: 1, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[4], paddingVertical: space[2] },
  deckFrame: { overflow: 'hidden', borderRadius: radius.lg, borderCurve: 'continuous', borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.12)', backgroundColor: '#111114', shadowColor: '#000000', shadowOpacity: 0.48, shadowRadius: 28, shadowOffset: { width: 0, height: 18 } },
  webView: { flex: 1, width: '100%', height: '100%', backgroundColor: '#111114', opacity: 1 },
  webViewHidden: { opacity: 0 },
  loading: { position: 'absolute', inset: 0, alignItems: 'center', justifyContent: 'center', gap: space[3], backgroundColor: '#111114' },
  loadingText: { ...type.caption, color: 'rgba(255,255,255,0.68)' },
  error: { position: 'absolute', inset: 0, alignItems: 'center', justifyContent: 'center', gap: space[2], paddingHorizontal: space[6], backgroundColor: '#111114' },
  errorIcon: { width: 42, height: 42, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: 'rgba(255,90,25,0.14)' },
  errorTitle: { ...type.headline, color: '#FFFFFF', textAlign: 'center' },
  errorBody: { ...type.caption, maxWidth: 340, color: 'rgba(255,255,255,0.62)', textAlign: 'center' },
  retry: { minHeight: hitMin, justifyContent: 'center', marginTop: space[2], paddingHorizontal: space[4], borderRadius: radius.full, backgroundColor: '#FFFFFF' },
  retryPressed: { opacity: 0.86, transform: [{ scale: 0.96 }] },
  retryText: { ...type.button, color: '#09090B' },
  controlsSlot: { minHeight: 76, alignItems: 'center', justifyContent: 'center', paddingBottom: space[2] },
  controls: { minHeight: 56, flexDirection: 'row', alignItems: 'center', gap: space[1], padding: 4, backgroundColor: 'rgba(8,8,10,0.58)' },
  controlButton: { width: 48, height: 48, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  controlDisabled: { opacity: 0.28 },
  controlPressed: { backgroundColor: 'rgba(255,255,255,0.12)', transform: [{ scale: 0.96 }] },
  slideCount: { ...type.captionMedium, minWidth: 64, color: '#FFFFFF', textAlign: 'center', fontVariant: ['tabular-nums'] },
});
