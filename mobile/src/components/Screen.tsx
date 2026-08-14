import React from 'react';
import {
  ActivityIndicator,
  RefreshControl,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
  type ViewStyle,
  type ScrollView as ScrollViewType,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useNavigation } from '@react-navigation/native';
import { SymbolView } from 'expo-symbols';
import { colors, space, type } from '../theme/tokens';

function scalableText<T extends { readonly lineHeight: number }>(value: T) {
  const { lineHeight: _fixedLineHeight, ...style } = value;
  return style;
}

type Props = {
  title?: string;
  subtitle?: string;
  children: React.ReactNode;
  loading?: boolean;
  error?: string | null;
  onRetry?: () => void;
  refreshing?: boolean;
  onRefresh?: () => void;
  scroll?: boolean;
  right?: React.ReactNode;
  style?: ViewStyle;
  scrollRef?: React.RefObject<ScrollViewType | null>;
};

/** Phone workspace chrome — topbar title scale from live tool titles. */
export function Screen({
  title,
  subtitle,
  children,
  loading,
  error,
  onRetry,
  refreshing,
  onRefresh,
  scroll = true,
  right,
  style,
  scrollRef,
}: Props) {
  const navigation = useNavigation();
  const canGoBack = navigation.canGoBack();
  const body = (
    <>
      {(title || subtitle || right) && (
        <View style={styles.header}>
          {canGoBack ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Go back"
              hitSlop={10}
              onPress={() => navigation.goBack()}
              style={({ pressed }) => [styles.back, pressed && styles.backPressed]}
            >
              <SymbolView name="chevron.left" tintColor={colors.text1} size={19} />
            </Pressable>
          ) : null}
          <View style={styles.headerText}>
            {title ? <Text maxFontSizeMultiplier={2} style={styles.title}>{title}</Text> : null}
            {subtitle ? <Text maxFontSizeMultiplier={2} style={styles.subtitle}>{subtitle}</Text> : null}
          </View>
          {right}
        </View>
      )}
      {error ? (
        <View style={styles.errorBox}>
          <Text style={styles.errorText}>{error}</Text>
          {onRetry ? (
            <Text style={styles.retry} onPress={onRetry}>
              Tap to retry
            </Text>
          ) : null}
        </View>
      ) : null}
      {loading && !refreshing ? (
        <View style={styles.loading}>
          <ActivityIndicator color={colors.accent} />
        </View>
      ) : (
        children
      )}
    </>
  );

  return (
    <SafeAreaView style={[styles.safe, style]} edges={['top', 'left', 'right']}>
      {scroll ? (
        <ScrollView
          ref={scrollRef}
          contentContainerStyle={styles.scroll}
          keyboardShouldPersistTaps="handled"
          refreshControl={
            onRefresh ? (
              <RefreshControl
                refreshing={Boolean(refreshing)}
                onRefresh={onRefresh}
                tintColor={colors.accent}
              />
            ) : undefined
          }
        >
          {body}
        </ScrollView>
      ) : (
        <View style={styles.fill}>{body}</View>
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  scroll: {
    paddingHorizontal: space[5],
    paddingTop: space[3],
    paddingBottom: space[10],
    flexGrow: 1,
  },
  fill: {
    flex: 1,
    paddingHorizontal: space[5],
    paddingTop: space[3],
  },
  header: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    marginBottom: space[4],
    gap: space[3],
  },
  headerText: {
    flex: 1,
  },
  back: {
    width: 42,
    height: 42,
    borderRadius: 15,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  backPressed: {
    opacity: 0.72,
    transform: [{ scale: 0.96 }],
  },
  title: {
    // Fixed token line-heights clip Google Sans at accessibility content
    // sizes. Omit the key entirely so optimized bundles also let the platform
    // derive the scaled line box.
    ...scalableText(type.title1),
    color: colors.text1,
  },
  subtitle: {
    marginTop: 4,
    ...scalableText(type.caption),
    color: colors.text2,
  },
  loading: {
    paddingVertical: space[10],
    alignItems: 'center',
  },
  errorBox: {
    backgroundColor: colors.dangerSoft,
    borderRadius: 12,
    padding: 14,
    marginBottom: space[4],
  },
  errorText: {
    color: colors.danger,
    ...scalableText(type.bodySm),
  },
  retry: {
    marginTop: 8,
    color: colors.accent,
    fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600',
    fontSize: 14,
  },
});
