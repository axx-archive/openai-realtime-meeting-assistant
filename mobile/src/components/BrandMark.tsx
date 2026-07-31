import React from 'react';
import { StyleSheet, View, type ColorValue, type ViewStyle } from 'react-native';
import { Image } from 'expo-image';
import Svg, { Path } from 'react-native-svg';
import { colors, radius, shadow } from '../theme/tokens';
import {
  STRIDE_WORDMARK_PATH,
  STRIDE_WORDMARK_RATIO,
  STRIDE_WORDMARK_VIEWBOX,
} from '../theme/strideWordmark';

const strideLogoMark = require('../../assets/stride-logo-mark.png');
const strideLogoBlack = require('../../assets/stride-logo-black.png');
const strideLogoWhite = require('../../assets/stride-logo-white.png');

type Props = {
  size?: number;
  style?: ViewStyle;
};

/** “The Strike” — the cropped momentum frame used by static identity surfaces. */
export function BrandMark({ size = 56, style }: Props) {
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
      <Image
        source={strideLogoMark}
        style={{ width: size, height: size }}
        contentFit="contain"
        cachePolicy="memory-disk"
      />
    </View>
  );
}

/** Compatibility colorways; The Strike tile is theme-invariant. */
export function StrideLogo({
  size = 44,
  tone = 'black',
}: {
  size?: number;
  tone?: 'black' | 'white';
}) {
  return (
    <Image
      source={tone === 'white' ? strideLogoWhite : strideLogoBlack}
      style={{ width: size, height: size }}
      contentFit="contain"
      cachePolicy="memory-disk"
    />
  );
}

/**
 * THE WORDMARK — the name as artwork, not as type.
 *
 * Drawn from the printed outline in theme/strideWordmark.ts, which the desktop
 * and the marketing site also draw, so the three cannot drift. `height` is the
 * only dimension a caller sets; the width follows from the traced aspect ratio,
 * because a wordmark that can be squashed independently in one axis is a
 * wordmark that eventually is.
 *
 * The colour is the receiving row's graphite, NEVER orange — orange is custody
 * of energy, and the name is the thing that never moves. See `colors.wordmark`.
 */
export function StrideWordmark({
  height = 18,
  color = colors.wordmark,
}: {
  height?: number;
  // ColorValue, not string: the token is a DynamicColorIOS pair so the mark
  // follows the system appearance without this component knowing the theme.
  color?: ColorValue;
}) {
  const width = height * STRIDE_WORDMARK_RATIO;
  return (
    <Svg
      width={width}
      height={height}
      viewBox={STRIDE_WORDMARK_VIEWBOX}
      accessibilityRole="image"
      accessibilityLabel="Stride"
    >
      <Path d={STRIDE_WORDMARK_PATH} fill={color} fillRule="evenodd" />
    </Svg>
  );
}

const styles = StyleSheet.create({
  mark: {
    overflow: 'hidden',
    backgroundColor: '#050505',
    alignItems: 'center',
    justifyContent: 'center',
  },
});
