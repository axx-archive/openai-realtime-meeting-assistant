import React from 'react';
import {
  ActivityIndicator,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
  type ViewStyle,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { colors, space, type } from '../theme/tokens';

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
}: Props) {
  const body = (
    <>
      {(title || subtitle || right) && (
        <View style={styles.header}>
          <View style={styles.headerText}>
            {title ? <Text style={styles.title}>{title}</Text> : null}
            {subtitle ? <Text style={styles.subtitle}>{subtitle}</Text> : null}
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
  title: {
    ...type.title1,
    color: colors.text1,
  },
  subtitle: {
    marginTop: 4,
    ...type.caption,
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
    ...type.bodySm,
  },
  retry: {
    marginTop: 8,
    color: colors.accent,
    fontWeight: '600',
    fontSize: 14,
  },
});
