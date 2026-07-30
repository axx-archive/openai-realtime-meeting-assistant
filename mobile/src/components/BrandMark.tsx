import React from 'react';
import { StyleSheet, View, type ViewStyle } from 'react-native';
import { Image } from 'expo-image';
import { radius, shadow } from '../theme/tokens';

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

const styles = StyleSheet.create({
  mark: {
    overflow: 'hidden',
    backgroundColor: '#050505',
    alignItems: 'center',
    justifyContent: 'center',
  },
});
