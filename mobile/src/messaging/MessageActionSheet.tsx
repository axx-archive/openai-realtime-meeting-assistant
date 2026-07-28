import React, { useEffect, useMemo, useRef } from 'react';
import { Animated, Modal, Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';

import { useReduceMotion, duration, ease } from '../theme/motion';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import type { ScoutMessageReaction } from '../api/types';

export const messageReactionChoices = ['❤️', '👍', '👎', '😂', '‼️', '❓', '🔥'] as const;

type Props = {
  visible: boolean;
  own: boolean;
  snippet: string;
  reactions: ScoutMessageReaction[];
  onClose: () => void;
  onReact: (emoji: string) => void;
  onReply: () => void;
  onEdit: () => void;
  onDelete: () => void;
};

export function MessageActionSheet({ visible, own, snippet, reactions, onClose, onReact, onReply, onEdit, onDelete }: Props) {
  const reduced = useReduceMotion();
  const progress = useRef(new Animated.Value(0)).current;
  const visibleReactions = useMemo(() => reactions.slice(0, 12), [reactions]);
  useEffect(() => {
    if (!visible) {
      progress.setValue(0);
      return;
    }
    Animated.timing(progress, {
      toValue: 1,
      duration: reduced ? 0 : duration.med,
      easing: ease,
      useNativeDriver: true,
    }).start();
  }, [progress, reduced, visible]);

  return (
    <Modal visible={visible} transparent animationType="none" onRequestClose={onClose}>
      <View style={styles.modal}>
        <Pressable accessibilityLabel="Close message actions" onPress={onClose} style={StyleSheet.absoluteFill}>
          <Animated.View style={[StyleSheet.absoluteFill, styles.scrim, { opacity: progress }]} />
        </Pressable>
        <View style={styles.sheet}>
          <Animated.View
            style={{
              opacity: progress.interpolate({ inputRange: [0, 0.72], outputRange: [0, 1], extrapolate: 'clamp' }),
              transform: [{ translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [8, 0] }) }],
            }}
          >
            <Text numberOfLines={2} style={styles.snippet}>{snippet || 'Attachment'}</Text>
          </Animated.View>
          <Animated.View
            accessibilityLabel="React to message"
            style={[
              styles.picker,
              {
                opacity: progress.interpolate({ inputRange: [0.08, 0.8], outputRange: [0, 1], extrapolate: 'clamp' }),
                transform: [
                  { translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [12, 0] }) },
                  { scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.96, 1] }) },
                ],
              },
            ]}
          >
            {messageReactionChoices.map((emoji) => (
              <Pressable
                key={emoji}
                accessibilityRole="button"
                accessibilityLabel={`React ${emoji}`}
                onPress={() => onReact(emoji)}
                style={({ pressed }) => [styles.reaction, pressed && styles.reactionPressed]}
              >
                <Text style={styles.emoji}>{emoji}</Text>
              </Pressable>
            ))}
          </Animated.View>
          {visibleReactions.length > 0 ? (
            <Animated.View
              accessibilityLabel="People who reacted"
              style={[
                styles.reactionPeople,
                {
                  opacity: progress.interpolate({ inputRange: [0.16, 0.88], outputRange: [0, 1], extrapolate: 'clamp' }),
                  transform: [{ translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [14, 0] }) }],
                },
              ]}
            >
              <Text style={styles.reactionPeopleTitle}>Reactions</Text>
              <View style={styles.reactionPeopleList}>
                {visibleReactions.map((reaction, index) => {
                  const actor = reaction.actorName?.trim() || reaction.actorEmail.split('@')[0] || 'Someone';
                  return (
                    <View key={`${reaction.actorEmail}-${reaction.emoji}-${index}`} style={styles.reactorChip}>
                      <Text style={styles.reactorEmoji}>{reaction.emoji}</Text>
                      <Text numberOfLines={1} style={styles.reactorName}>{actor}</Text>
                    </View>
                  );
                })}
                {reactions.length > visibleReactions.length ? (
                  <Text style={styles.moreReactions}>+{reactions.length - visibleReactions.length}</Text>
                ) : null}
              </View>
            </Animated.View>
          ) : null}
          <Animated.View
          style={[
            styles.actions,
            {
              opacity: progress.interpolate({ inputRange: [0.22, 1], outputRange: [0, 1], extrapolate: 'clamp' }),
              transform: [
                { translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [18, 0] }) },
              ],
            },
          ]}
          >
              <Pressable accessibilityRole="button" accessibilityLabel="Reply to message" onPress={onReply} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                <SymbolView name="arrowshape.turn.up.left" tintColor={colors.text1} size={18} />
                <Text style={styles.actionText}>Reply</Text>
              </Pressable>
              {own ? <View style={styles.rule} /> : null}
              {own ? (
                <>
              <Pressable accessibilityRole="button" onPress={onEdit} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                <SymbolView name="pencil" tintColor={colors.text1} size={18} />
                <Text style={styles.actionText}>Edit message</Text>
              </Pressable>
              <View style={styles.rule} />
              <Pressable accessibilityRole="button" onPress={onDelete} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                <SymbolView name="trash" tintColor={colors.danger} size={18} />
                <Text style={[styles.actionText, styles.deleteText]}>Delete message</Text>
              </Pressable>
                </>
              ) : null}
          </Animated.View>
        </View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  modal: { flex: 1, justifyContent: 'flex-end', paddingHorizontal: space[4], paddingBottom: space[8] },
  scrim: { backgroundColor: colors.scrim },
  sheet: { ...shadow.glass, gap: space[3] },
  snippet: {
    ...type.caption,
    alignSelf: 'center',
    maxWidth: '86%',
    paddingHorizontal: space[4],
    paddingVertical: space[2],
    overflow: 'hidden',
    borderRadius: radius.full,
    color: colors.text2,
    backgroundColor: colors.surface1,
  },
  picker: {
    flexDirection: 'row',
    alignSelf: 'center',
    padding: 5,
    borderRadius: radius.full,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
  },
  reaction: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22 },
  reactionPressed: { backgroundColor: colors.accentSoft, transform: [{ scale: 0.96 }] },
  emoji: { fontSize: 25, lineHeight: 31 },
  reactionPeople: {
    gap: space[2],
    padding: space[3],
    borderRadius: radius.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface1,
  },
  reactionPeopleTitle: { ...type.captionMedium, color: colors.text2 },
  reactionPeopleList: { flexDirection: 'row', flexWrap: 'wrap', gap: 6 },
  reactorChip: { maxWidth: 132, minHeight: 32, flexDirection: 'row', alignItems: 'center', gap: 5, paddingHorizontal: 9, borderRadius: radius.full, backgroundColor: colors.surface3 },
  reactorEmoji: { fontSize: 16, lineHeight: 21 },
  reactorName: { ...type.caption, flexShrink: 1, color: colors.text1 },
  moreReactions: { ...type.captionMedium, alignSelf: 'center', color: colors.text2 },
  actions: {
    overflow: 'hidden',
    borderRadius: radius.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface1,
  },
  action: { minHeight: hitMin + 8, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[4] },
  actionPressed: { backgroundColor: colors.surface3 },
  actionText: { ...type.bodyMedium, color: colors.text1 },
  deleteText: { color: colors.danger },
  rule: { height: StyleSheet.hairlineWidth, marginLeft: 50, backgroundColor: colors.line1 },
});
