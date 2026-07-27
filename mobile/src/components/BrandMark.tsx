import React from 'react';
import { StyleSheet, View, type ViewStyle } from 'react-native';
import { Image } from 'expo-image';
import { radius, shadow } from '../theme/tokens';

const momentumMark = require('../../assets/bonfire-stride-mark.png');
const momentumGlyph = require('../../assets/android-icon-monochrome.png');

type Props = {
  size?: number;
  style?: ViewStyle;
};

/** Canonical human-and-agent momentum mark used throughout the native app. */
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
        source={momentumMark}
        style={{ width: size, height: size }}
        contentFit="contain"
        cachePolicy="memory-disk"
      />
    </View>
  );
}

type GlyphProps = {
  size?: number;
  color: string;
};

/** Tintable momentum silhouette for compact navigation chrome. */
export function MomentumGlyph({ size = 22, color }: GlyphProps) {
  return (
    <Image
      source={momentumGlyph}
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
