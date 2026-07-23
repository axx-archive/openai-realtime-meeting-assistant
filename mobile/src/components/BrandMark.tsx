import React from 'react';
import { StyleSheet, View, type ViewStyle } from 'react-native';
import Svg, { Path } from 'react-native-svg';
import { colors, radius, shadow } from '../theme/tokens';

type Props = {
  size?: number;
  style?: ViewStyle;
};

/**
 * Live app icon: ink tile + flame mark from `public/app-icon.svg` / login-mark.
 */
export function BrandMark({ size = 56, style }: Props) {
  const flame = Math.round(size * 0.48);
  const r = size <= 44 ? radius.lg : 16;
  return (
    <View
      style={[
        styles.mark,
        shadow.mark,
        {
          width: size,
          height: size,
          borderRadius: r,
        },
        style,
      ]}
    >
      <Svg width={flame} height={flame} viewBox="0 0 64 64" fill="none">
        <Path
          fill={colors.onAccent}
          fillRule="evenodd"
          clipRule="evenodd"
          d="M32 5c2 10 18 16.5 18 34a18 18 0 1 1-36 0C14 21.5 30 15 32 5Zm0 25c1.2 6 9 8.5 9 16a9 9 0 1 1-18 0c0-7.5 7.8-10 9-16Z"
        />
      </Svg>
    </View>
  );
}

const styles = StyleSheet.create({
  mark: {
    backgroundColor: colors.accent,
    alignItems: 'center',
    justifyContent: 'center',
  },
});
