import React, { useState, useCallback } from 'react';
import { Platform, Pressable, StyleSheet, Text, View, useWindowDimensions } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { ChannelList } from '../messaging/ChannelList';
import { ThreadScreen } from './ThreadScreen';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type ChatNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * iPad split threshold — list/thread split at this width (portrait iPad).
 *
 * 744pt matches the slim rail breakpoint. On iPad 11" (~834pt portrait) and
 * 13" (~1024pt portrait), Chat shows a real list/thread split, not a stretched
 * phone. The split is NOT only at ≥1024 — it activates in portrait too.
 */
const SPLIT_VIEW_MIN_WIDTH = 744;

/**
 * iPad workstation threshold — New conversation uses card presentation, not sheet.
 *
 * ≥1024pt (iPad Pro 11" landscape, iPad Pro 12.9" portrait): workstation surface.
 * <1024pt: phone sheet. Chat split at ≥744 still holds separately.
 */
const WORKSTATION_MIN_WIDTH = 1024;

/**
 * The Chat destination — list/thread split on iPad portrait.
 *
 * Phone: fills the content area with the list; tapping opens Thread full-screen.
 * iPad (≥744pt): shows list on left + selected thread on right — a real split
 * view, not a 248pt labeled list or a stretched phone.
 *
 * New conversation is available directly from Chat, not only from Work.
 */

export function ChatScreen() {
  const navigation = useNavigation<ChatNav>();
  const { width } = useWindowDimensions();
  const isIPad = Platform.OS === 'ios' && Platform.isPad;
  const showSplit = isIPad && width >= SPLIT_VIEW_MIN_WIDTH;
  const useWorkstation = isIPad && width >= WORKSTATION_MIN_WIDTH;
  const [selectedThreadId, setSelectedThreadId] = useState<string | null>(null);

  const handleOpenThread = useCallback((thread: { id: string; title?: string }) => {
    if (showSplit) {
      setSelectedThreadId(thread.id);
    } else {
      navigation.navigate('Thread', { threadId: thread.id, title: thread.title ?? '' });
    }
  }, [showSplit, navigation]);

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      {showSplit ? (
        <View style={styles.splitContainer}>
          <View style={styles.listPane}>
            <View style={styles.header}>
              <Text accessibilityRole="header" style={styles.title}>Chat</Text>
              <View style={styles.headerActions}>
                <Pressable
                  accessibilityLabel="Notifications"
                  accessibilityRole="button"
                  accessibilityHint="Opens your notifications and alerts"
                  onPress={() => navigation.navigate('Alerts')}
                  style={({ pressed }) => [styles.bellButton, pressed && styles.bellPressed]}
                >
                  <SymbolView name="bell.fill" size={18} tintColor={colors.text2} />
                </Pressable>
                <Pressable
                  accessibilityLabel="New conversation"
                  accessibilityRole="button"
                  onPress={() => navigation.navigate('NewConversation', { displayMode: useWorkstation ? 'workstation' : 'sheet' })}
                  style={({ pressed }) => [styles.createButton, pressed && styles.pressed]}
                >
                  <SymbolView name="square.and.pencil" size={16} tintColor={colors.onAccent} />
                  <Text style={styles.createText}>New</Text>
                </Pressable>
              </View>
            </View>
            <View style={styles.listContainerSplit}>
              <ChannelList onOpenThread={handleOpenThread} selectedThreadId={selectedThreadId ?? undefined} />
            </View>
          </View>
          <View style={styles.threadPane}>
            {selectedThreadId ? (
              <ThreadScreen
                route={{ params: { threadId: selectedThreadId, title: '' }, key: 'embedded-thread', name: 'Thread' }}
                navigation={navigation as any}
              />
            ) : (
              <View style={styles.emptyPane}>
                <SymbolView name="bubble.left.and.bubble.right" size={48} tintColor={colors.text3} />
                <Text style={styles.emptyText}>Select a conversation</Text>
              </View>
            )}
          </View>
        </View>
      ) : (
        <>
          <View style={styles.header}>
            <Text accessibilityRole="header" style={styles.title}>Chat</Text>
            <View style={styles.headerActions}>
              <Pressable
                accessibilityLabel="Notifications"
                accessibilityRole="button"
                accessibilityHint="Opens your notifications and alerts"
                onPress={() => navigation.navigate('Alerts')}
                style={({ pressed }) => [styles.bellButton, pressed && styles.bellPressed]}
              >
                <SymbolView name="bell.fill" size={18} tintColor={colors.text2} />
              </Pressable>
              <Pressable
                accessibilityLabel="New conversation"
                accessibilityRole="button"
                onPress={() => navigation.navigate('NewConversation', { displayMode: useWorkstation ? 'workstation' : 'sheet' })}
                style={({ pressed }) => [styles.createButton, pressed && styles.pressed]}
              >
                <SymbolView name="square.and.pencil" size={16} tintColor={colors.onAccent} />
                <Text style={styles.createText}>New</Text>
              </Pressable>
            </View>
          </View>
          <View style={styles.listContainer}>
            <ChannelList onOpenThread={handleOpenThread} />
          </View>
        </>
      )}
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
  headerActions: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
  },
  bellButton: {
    width: 36,
    height: 36,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
  },
  bellPressed: {
    opacity: 0.7,
    backgroundColor: colors.surface2,
  },
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
  // iPad split view styles
  splitContainer: {
    flex: 1,
    flexDirection: 'row',
  },
  listPane: {
    width: 320,
    minWidth: 280,
    maxWidth: 400,
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: colors.line1,
  },
  listContainerSplit: {
    flex: 1,
    minHeight: 0,
    paddingHorizontal: space[1],
  },
  threadPane: {
    flex: 1,
    minWidth: 0,
    backgroundColor: colors.bgApp,
  },
  emptyPane: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[3],
  },
  emptyText: {
    ...type.body,
    color: colors.text3,
  },
});
