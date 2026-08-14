import React from 'react';
import {
  Pressable,
  StyleSheet,
  Text,
  View,
  useWindowDimensions,
  type AccessibilityState,
  type ViewStyle,
} from 'react-native';
import { colors, radius, shadow, space, type } from '../theme/tokens';

function scalableText<T extends { readonly lineHeight: number }>(value: T) {
  const { lineHeight: _fixedLineHeight, ...style } = value;
  return style;
}

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
  const { fontScale } = useWindowDimensions();
  const largeText = Number.isFinite(fontScale) && fontScale >= 1.35;
  const content = (
    <>
      <View style={[styles.row, largeText && styles.rowLargeText]}>
        <Text maxFontSizeMultiplier={2} style={styles.title}>
          {title}
        </Text>
        {badge ? (
          <View
            style={[
              styles.badge,
              largeText && styles.badgeLargeText,
              badgeTone === 'live' && styles.badgeLive,
              badgeTone === 'warn' && styles.badgeWarn,
            ]}
          >
            <Text
              maxFontSizeMultiplier={2}
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
        <Text maxFontSizeMultiplier={2} style={styles.subtitle}>
          {subtitle}
        </Text>
      ) : null}
      {meta ? <Text maxFontSizeMultiplier={2} style={styles.meta}>{meta}</Text> : null}
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
  rowLargeText: {
    flexDirection: 'column',
    alignItems: 'stretch',
  },
  title: {
    flex: 1,
    // Fixed token line-heights clip Google Sans at accessibility content
    // sizes. Let the platform derive a scaled line box and keep the full
    // card copy available instead of truncating it by line count.
    ...scalableText(type.headline),
    color: colors.text1,
  },
  subtitle: {
    marginTop: 6,
    ...scalableText(type.bodySm),
    color: colors.text2,
  },
  meta: {
    marginTop: 10,
    ...scalableText(type.label),
    color: colors.text3,
    textTransform: 'none',
  },
  badge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
  },
  badgeLargeText: {
    alignSelf: 'flex-start',
  },
  badgeLive: {
    backgroundColor: colors.liveSoft,
  },
  badgeWarn: {
    backgroundColor: colors.warnSoft,
  },
  badgeText: {
    ...scalableText(type.label),
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
