import React, { useEffect, useMemo, useRef } from 'react';
import { Animated, Modal, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';

import { useReduceMotion, duration, ease } from '../theme/motion';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import type { ScoutMessageReaction } from '../api/types';
import {
  messageReactionButtonSize,
  messageReactionChoices,
  messageReactionTrayHeight,
  messageReactionTrayPadding,
} from './messageGestures';

type Props = {
  visible: boolean;
  contained?: boolean;
  own: boolean;
  snippet: string;
  reactions: ScoutMessageReaction[];
  onClose: () => void;
  onReact?: (emoji: string) => void;
  onCopy: () => void;
  onSaveAttachment?: () => void;
  onReply?: () => void;
  onRiffPrivately?: () => void;
  onShareFromRiff?: () => void;
  onEdit?: () => void;
  onDelete?: () => void;
  onChangeProject?: () => void;
  projectChangePending?: boolean;
};

export function MessageActionSheet({ visible, contained = false, own, snippet, reactions, onClose, onReact, onCopy, onSaveAttachment, onReply, onRiffPrivately, onShareFromRiff, onEdit, onDelete, onChangeProject, projectChangePending = false }: Props) {
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

  const content = (
    <View style={[styles.modal, contained && styles.containedModal]}>
      <Pressable accessibilityLabel="Close message actions" onPress={onClose} style={StyleSheet.absoluteFill}>
        <Animated.View style={[StyleSheet.absoluteFill, styles.scrim, { opacity: progress }]} />
      </Pressable>
        <View style={styles.sheet}>
          {onReact ? <Animated.View
            style={{
              opacity: progress.interpolate({ inputRange: [0, 0.72], outputRange: [0, 1], extrapolate: 'clamp' }),
              transform: [{ translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [8, 0] }) }],
            }}
          >
            <Text numberOfLines={2} style={styles.snippet}>{snippet || 'Attachment'}</Text>
          </Animated.View> : null}
          <Animated.View
            accessibilityLabel="React to message"
            style={[
              styles.pickerShell,
              {
                opacity: progress.interpolate({ inputRange: [0.08, 0.8], outputRange: [0, 1], extrapolate: 'clamp' }),
                transform: [
                  { translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [12, 0] }) },
                  { scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.96, 1] }) },
                ],
              },
            ]}
          >
            <ScrollView
              horizontal
              bounces={false}
              showsHorizontalScrollIndicator={false}
              style={styles.pickerScroll}
              contentContainerStyle={styles.picker}
            >
              {messageReactionChoices.map((emoji) => (
                <Pressable
                  key={emoji}
                  accessibilityRole="button"
                  accessibilityLabel={`React ${emoji}`}
                  onPress={() => onReact?.(emoji)}
                  style={({ pressed }) => [styles.reaction, pressed && styles.reactionPressed]}
                >
                  <Text style={styles.emoji}>{emoji}</Text>
                </Pressable>
              ))}
            </ScrollView>
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
              {snippet.trim() && !onSaveAttachment ? (
                <>
                  <Pressable accessibilityRole="button" accessibilityLabel="Copy message" onPress={onCopy} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                    <SymbolView name="doc.on.doc" tintColor={colors.text1} size={18} />
                    <Text style={styles.actionText}>Copy message</Text>
                  </Pressable>
                  <View style={styles.rule} />
                </>
              ) : null}
              {onSaveAttachment ? (
                <>
                  <Pressable accessibilityRole="button" accessibilityLabel="Save attachment to Drive" onPress={onSaveAttachment} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                    <SymbolView name="externaldrive.fill" tintColor={colors.text1} size={18} />
                    <Text style={styles.actionText}>Save to Drive</Text>
                  </Pressable>
                  <View style={styles.rule} />
                </>
              ) : null}
              {onReply ? <Pressable accessibilityRole="button" accessibilityLabel="Reply to message" onPress={onReply} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                <SymbolView name="arrowshape.turn.up.left" tintColor={colors.text1} size={18} />
                <Text style={styles.actionText}>Reply</Text>
              </Pressable> : null}
              {onRiffPrivately ? <View style={styles.rule} /> : null}
              {onRiffPrivately ? (
                <Pressable accessibilityRole="button" accessibilityLabel="Riff privately from this message" onPress={onRiffPrivately} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                  <SymbolView name="guitars.fill" tintColor={colors.emberText} size={18} />
                  <Text style={styles.actionText}>Riff privately from here</Text>
                </Pressable>
              ) : null}
              {onShareFromRiff ? <View style={styles.rule} /> : null}
              {onShareFromRiff ? (
                <Pressable accessibilityRole="button" accessibilityLabel="Share this Private Riff reply to its source channel" onPress={onShareFromRiff} style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}>
                  <SymbolView name="paperplane.fill" tintColor={colors.emberText} size={18} />
                  <Text style={styles.actionText}>Share this reply to source</Text>
                </Pressable>
              ) : null}
              {own && onChangeProject ? <View style={styles.rule} /> : null}
              {own && onChangeProject ? (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Change project for this message"
                  accessibilityState={{ disabled: projectChangePending }}
                  disabled={projectChangePending}
                  onPress={onChangeProject}
                  style={({ pressed }) => [styles.action, pressed && styles.actionPressed, projectChangePending && styles.actionDisabled]}
                >
                  <SymbolView name="folder" tintColor={colors.text1} size={18} />
                  <Text style={styles.actionText}>Change project</Text>
                </Pressable>
              ) : null}
              {own && onEdit && onDelete ? <View style={styles.rule} /> : null}
              {own && onEdit && onDelete ? (
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
  );

  // A thread is already hosted inside an iOS page-sheet Modal. Presenting a
  // second sibling Modal from the screen underneath it is unreliable on iOS
  // and can leave the action surface invisible. Keep reply actions inside the
  // currently presented sheet; the main feed still uses the native Modal.
  if (contained) return visible ? content : null;

  return (
    <Modal visible={visible} transparent animationType="none" onRequestClose={onClose}>
      {content}
    </Modal>
  );
}

const styles = StyleSheet.create({
  modal: { flex: 1, justifyContent: 'flex-end', paddingHorizontal: space[4], paddingBottom: space[8] },
  containedModal: { position: 'absolute', inset: 0, zIndex: 100, elevation: 100 },
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
  pickerShell: {
    alignSelf: 'center',
    flexGrow: 0,
    height: messageReactionTrayHeight,
    maxWidth: '100%',
    borderRadius: radius.full,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    overflow: 'hidden',
  },
  pickerScroll: { flexGrow: 0 },
  picker: { flexDirection: 'row', padding: messageReactionTrayPadding },
  reaction: {
    width: messageReactionButtonSize,
    height: messageReactionButtonSize,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: messageReactionButtonSize / 2,
  },
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
  actionDisabled: { opacity: 0.48 },
  actionText: { ...type.bodyMedium, color: colors.text1 },
  deleteText: { color: colors.danger },
  rule: { height: StyleSheet.hairlineWidth, marginLeft: 50, backgroundColor: colors.line1 },
});
