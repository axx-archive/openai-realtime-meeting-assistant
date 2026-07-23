import React from 'react';
import { Pressable, StyleSheet, Text, View, type ViewStyle } from 'react-native';
import { colors } from '../theme/colors';

type Props = {
  title: string;
  subtitle?: string;
  meta?: string;
  badge?: string;
  badgeTone?: 'live' | 'muted' | 'warn';
  onPress?: () => void;
  style?: ViewStyle;
  children?: React.ReactNode;
};

export function Card({
  title,
  subtitle,
  meta,
  badge,
  badgeTone = 'muted',
  onPress,
  style,
  children,
}: Props) {
  const content = (
    <>
      <View style={styles.row}>
        <Text style={styles.title} numberOfLines={2}>
          {title}
        </Text>
        {badge ? (
          <View
            style={[
              styles.badge,
              badgeTone === 'live' && styles.badgeLive,
              badgeTone === 'warn' && styles.badgeWarn,
            ]}
          >
            <Text
              style={[
                styles.badgeText,
                badgeTone === 'live' && styles.badgeTextLive,
                badgeTone === 'warn' && styles.badgeTextWarn,
              ]}
            >
              {badge}
            </Text>
          </View>
        ) : null}
      </View>
      {subtitle ? (
        <Text style={styles.subtitle} numberOfLines={3}>
          {subtitle}
        </Text>
      ) : null}
      {meta ? <Text style={styles.meta}>{meta}</Text> : null}
      {children}
    </>
  );

  if (onPress) {
    return (
      <Pressable
        onPress={onPress}
        style={({ pressed }) => [styles.card, pressed && styles.pressed, style]}
      >
        {content}
      </Pressable>
    );
  }

  return <View style={[styles.card, style]}>{content}</View>;
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: colors.bgElevated,
    borderRadius: 16,
    padding: 16,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
    marginBottom: 12,
    shadowColor: '#0E0E10',
    shadowOpacity: 0.06,
    shadowRadius: 12,
    shadowOffset: { width: 0, height: 4 },
  },
  pressed: {
    opacity: 0.88,
    transform: [{ scale: 0.995 }],
  },
  row: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: 10,
  },
  title: {
    flex: 1,
    fontSize: 17,
    fontWeight: '600',
    color: colors.text,
    letterSpacing: -0.2,
  },
  subtitle: {
    marginTop: 6,
    fontSize: 14,
    lineHeight: 20,
    color: colors.textSecondary,
  },
  meta: {
    marginTop: 10,
    fontSize: 12,
    color: colors.textTertiary,
  },
  badge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 999,
    backgroundColor: colors.bgMuted,
  },
  badgeLive: {
    backgroundColor: colors.liveSoft,
  },
  badgeWarn: {
    backgroundColor: 'rgba(255, 159, 10, 0.14)',
  },
  badgeText: {
    fontSize: 11,
    fontWeight: '600',
    color: colors.textSecondary,
    textTransform: 'uppercase',
    letterSpacing: 0.4,
  },
  badgeTextLive: {
    color: colors.live,
  },
  badgeTextWarn: {
    color: colors.warn,
  },
});
