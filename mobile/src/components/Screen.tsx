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
import { colors } from '../theme/colors';

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
    backgroundColor: colors.bg,
  },
  scroll: {
    padding: 20,
    paddingBottom: 40,
    flexGrow: 1,
  },
  fill: {
    flex: 1,
    paddingHorizontal: 20,
    paddingTop: 12,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    marginBottom: 16,
    gap: 12,
  },
  headerText: {
    flex: 1,
  },
  title: {
    fontSize: 28,
    fontWeight: '600',
    letterSpacing: -0.6,
    color: colors.text,
  },
  subtitle: {
    marginTop: 4,
    fontSize: 15,
    color: colors.textSecondary,
  },
  loading: {
    paddingVertical: 40,
    alignItems: 'center',
  },
  errorBox: {
    backgroundColor: colors.dangerSoft,
    borderRadius: 12,
    padding: 14,
    marginBottom: 16,
  },
  errorText: {
    color: colors.danger,
    fontSize: 14,
    lineHeight: 20,
  },
  retry: {
    marginTop: 8,
    color: colors.accent,
    fontWeight: '600',
    fontSize: 14,
  },
});
