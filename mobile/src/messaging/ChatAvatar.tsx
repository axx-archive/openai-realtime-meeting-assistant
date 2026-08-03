import React from 'react';
import { StyleSheet, Text, View } from 'react-native';
import { Image } from 'expo-image';

import { colors } from '../theme/tokens';

type Props = {
  name: string;
  avatarDataURL?: string;
  size?: number;
};

export function ChatAvatar({ name, avatarDataURL, size = 28 }: Props) {
  const style = { width: size, height: size, borderRadius: size / 2 };
  if (avatarDataURL?.startsWith('data:image/')) {
    return (
      <Image
        accessible={false}
        source={{ uri: avatarDataURL }}
        contentFit="cover"
        cachePolicy="memory"
        enforceEarlyResizing
        recyclingKey={avatarDataURL}
        style={[styles.avatar, style]}
      />
    );
  }
  return (
    <View accessible={false} style={[styles.avatar, styles.fallback, style]}>
      <Text style={[styles.initial, { fontSize: Math.max(10, Math.round(size * 0.4)) }]}>
        {(name.trim().slice(0, 1) || '?').toUpperCase()}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  avatar: { borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface3 },
  fallback: { alignItems: 'center', justifyContent: 'center' },
  initial: { fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', color: colors.text2 },
});
