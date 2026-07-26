import React from 'react';
import {
  Pressable,
  StyleSheet,
  Text,
  View,
  type AccessibilityState,
  type ViewStyle,
} from 'react-native';
import { colors, radius, shadow, space, type } from '../theme/tokens';

type Props = {
  title: string;
  subtitle?: string;
  meta?: string;
  badge?: string;
  badgeTone?: 'live' | 'muted' | 'warn';
  onPress?: () => void;
  accessibilityLabel?: string;
  accessibilityHint?: string;
  accessibilityState?: AccessibilityState;
  style?: ViewStyle;
  children?: React.ReactNode;
};

/** Surface card — live --r-lg, hairline, soft shadow (no warm/parchment). */
export function Card({
  title,
  subtitle,
  meta,
  badge,
  badgeTone = 'muted',
  onPress,
  accessibilityLabel,
  accessibilityHint,
  accessibilityState,
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
        accessibilityRole="button"
        accessibilityLabel={accessibilityLabel ?? [title, subtitle, meta, badge].filter(Boolean).join('. ')}
        accessibilityHint={accessibilityHint ?? `Opens ${title}.`}
        accessibilityState={accessibilityState}
        onPress={onPress}
        style={({ pressed }) => [
          styles.card,
          shadow[1],
          pressed && styles.pressed,
          style,
        ]}
      >
        {content}
      </Pressable>
    );
  }

  return <View style={[styles.card, shadow[1], style]}>{content}</View>;
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: colors.surface1,
    borderRadius: radius.lg,
    padding: space[4],
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    marginBottom: space[3],
  },
  pressed: {
    opacity: 0.92,
    transform: [{ scale: 0.96 }],
  },
  row: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: 10,
  },
  title: {
    flex: 1,
    ...type.headline,
    color: colors.text1,
  },
  subtitle: {
    marginTop: 6,
    ...type.bodySm,
    color: colors.text2,
  },
  meta: {
    marginTop: 10,
    ...type.label,
    color: colors.text3,
    textTransform: 'none',
  },
  badge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
  },
  badgeLive: {
    backgroundColor: colors.liveSoft,
  },
  badgeWarn: {
    backgroundColor: colors.warnSoft,
  },
  badgeText: {
    ...type.label,
    color: colors.text2,
    textTransform: 'uppercase',
  },
  badgeTextLive: {
    color: colors.live,
  },
  badgeTextWarn: {
    color: colors.warn,
  },
});
