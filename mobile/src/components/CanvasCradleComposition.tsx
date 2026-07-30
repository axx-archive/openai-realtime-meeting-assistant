import React from 'react';
import { StyleSheet, View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { StrideCradle } from './StrideCradle';
import { colors, radius, space } from '../theme/tokens';

const EMPTY_TRACE: readonly number[] = [];

/**
 * Shared geometry for native launch and the live Canvas hero. Keeping these
 * values in one module is what lets the static boot cradle dissolve directly
 * into the real instrument without a positional jump.
 */
export const canvasCradleComposition = StyleSheet.create({
  body: {
    flexGrow: 1,
    alignItems: 'center',
    paddingHorizontal: space[6],
  },
  skyAbove: { flex: 1.25, pointerEvents: 'none' },
  skyBelow: { flex: 1, pointerEvents: 'none' },
  wave: {
    alignSelf: 'stretch',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: space[4],
    paddingVertical: 14,
    borderRadius: radius.xxl,
    borderCurve: 'continuous',
    marginBottom: space[5],
  },
  copyBlock: {
    minHeight: 104,
    alignItems: 'center',
  },
});

export function LaunchCradle() {
  return (
    <SafeAreaView
      style={styles.safe}
      edges={['top', 'left', 'right', 'bottom']}
      pointerEvents="none"
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      <View style={[canvasCradleComposition.body, styles.body]}>
        <View style={canvasCradleComposition.skyAbove} />
        <View style={canvasCradleComposition.wave}>
          <StrideCradle trace={EMPTY_TRACE} listening={false} />
        </View>
        <View style={canvasCradleComposition.copyBlock} />
        <View style={canvasCradleComposition.skyBelow} />
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  body: { flex: 1 },
});
