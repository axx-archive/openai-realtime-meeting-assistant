import React from 'react';
import { Pressable, StyleSheet, Text, View, useWindowDimensions } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { ChannelList } from '../messaging/ChannelList';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';
import { NATIVE_SHELL_SIDEBAR_MIN_WIDTH } from '../navigation/nativeShellModel';

type ChatNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Chat destination — a card surface for threads and conversations.
 *
 * This is a proper destination, not a peek sheet. Selecting Chat fills the
 * content area on phone (with the rail visible) and shows sidebar + list on
 * tablet ≥744. The Work segment is deliberately absent; Chat is purely about
 * conversations, not the artifacts and tools that live in Work.
 *
 * New conversation is available directly from Chat, not only from Work.
 */

export function ChatScreen() {
  const navigation = useNavigation<ChatNav>();
  const { width } = useWindowDimensions();
  const tablet = width >= NATIVE_SHELL_SIDEBAR_MIN_WIDTH;

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      <View style={styles.header}>
        <Text accessibilityRole="header" style={styles.title}>Chat</Text>
        <Pressable
          accessibilityLabel="New conversation"
          accessibilityRole="button"
          onPress={() => navigation.navigate('NewConversation')}
          style={({ pressed }) => [styles.createButton, pressed && styles.pressed]}
        >
          <SymbolView name="square.and.pencil" size={16} tintColor={colors.onAccent} />
          {tablet ? <Text style={styles.createText}>New conversation</Text> : null}
        </Pressable>
      </View>

      <View style={styles.listContainer}>
        <ChannelList />
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  header: {
    minHeight: 60,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: space[5],
    paddingTop: space[2],
  },
  title: { ...type.title1, color: colors.text1 },
  createButton: {
    minHeight: 36,
    minWidth: 36,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    backgroundColor: colors.accent,
  },
  createText: { ...type.button, color: colors.onAccent, fontSize: 14 },
  listContainer: {
    flex: 1,
    minHeight: 0,
    width: '100%',
    maxWidth: 820,
    alignSelf: 'center',
    paddingHorizontal: space[1],
  },
  pressed: { opacity: 0.82, transform: [{ scale: 0.98 }] },
});
