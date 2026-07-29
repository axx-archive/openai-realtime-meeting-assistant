import React from 'react';
import { StyleSheet, View, type ViewStyle } from 'react-native';
import { Image } from 'expo-image';
import { radius, shadow } from '../theme/tokens';

const strideSignalMark = require('../../assets/stride-signal-mark.png');
const strideSignalGlyph = require('../../assets/android-icon-monochrome.png');

type Props = {
  size?: number;
  style?: ViewStyle;
};

/** Canonical Stride Signal mark used throughout the native app. */
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
        source={strideSignalMark}
        style={{ width: size, height: size }}
        contentFit="contain"
        cachePolicy="memory-disk"
      />
    </View>
  );
}

type GlyphProps = {
  size?: number;
  /**
   * A resolved color string, NOT a semantic token: `expo-image`'s `tintColor`
   * rejects `DynamicColorIOS` values, so callers resolve light/dark themselves
   * (see `markTint` in CanvasScreen).
   */
  color: string;
};

/** Tintable Stride Signal for compact navigation chrome. */
export function StrideSignalGlyph({ size = 22, color }: GlyphProps) {
  return (
    <Image
      source={strideSignalGlyph}
      style={{ width: size, height: size }}
      contentFit="contain"
      tintColor={color}
      cachePolicy="memory-disk"
    />
  );
}

const styles = StyleSheet.create({
  mark: {
    overflow: 'hidden',
    backgroundColor: '#0E0E10',
    alignItems: 'center',
    justifyContent: 'center',
  },
});
