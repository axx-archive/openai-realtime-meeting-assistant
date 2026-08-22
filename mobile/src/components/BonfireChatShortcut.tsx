import React from 'react';
import { ActivityIndicator, Pressable, StyleSheet, View } from 'react-native';
import Svg, { Circle, Path } from 'react-native-svg';

import { Glass } from '../theme/glass';
import { colors, radius, shadow } from '../theme/tokens';

type Props = {
  busy?: boolean;
  onPress: () => void;
};

function EmberChatMark() {
  return (
    <Svg accessibilityElementsHidden width={30} height={30} viewBox="0 0 30 30" fill="none">
      <Path
        d="M6.5 7.5c0-1.1.9-2 2-2h13c1.1 0 2 .9 2 2v9c0 1.1-.9 2-2 2h-6.8l-5.2 4.4v-4.4h-1c-1.1 0-2-.9-2-2v-9Z"
        stroke="#FFFFFF"
        strokeWidth={1.9}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <Path
        d="M15 9.2c-2.2 1.4-3.3 2.8-3.3 4.4a3.3 3.3 0 0 0 6.6 0c0-1.6-1.1-3-3.3-4.4Z"
        stroke="#FFFFFF"
        strokeWidth={1.55}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <Circle cx={15} cy={14.1} r={1.05} fill="#FFFFFF" />
    </Svg>
  );
}

export function BonfireChatShortcut({ busy = false, onPress }: Props) {
  return (
    <Glass interactive radius={32} tint={colors.ember} style={styles.glass}>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel="Open Bonfire Chat"
        accessibilityHint="Goes directly to the company Bonfire Chat channel"
        accessibilityState={{ busy }}
        disabled={busy}
        onPress={onPress}
        style={({ pressed }) => [styles.button, pressed && styles.pressed, busy && styles.busy]}
      >
        <View accessibilityElementsHidden style={styles.emberCore}>
          {busy ? <ActivityIndicator color="#FFFFFF" size="small" /> : <EmberChatMark />}
        </View>
      </Pressable>
    </Glass>
  );
}

const styles = StyleSheet.create({
  glass: {
    position: 'absolute',
    left: 20,
    bottom: 20,
    zIndex: 20,
    width: 64,
    height: 64,
    ...shadow.glass,
  },
  button: {
    width: 64,
    height: 64,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 32,
    borderCurve: 'continuous',
  },
  emberCore: {
    width: 52,
    height: 52,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 26,
    borderCurve: 'continuous',
    backgroundColor: colors.ember,
  },
  pressed: { opacity: 0.9, transform: [{ scale: 0.96 }] },
  busy: { opacity: 0.72 },
});
