import React from 'react';
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';

import type { PrivateRiffBinding, ScoutMessage } from '../api/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import {
  privateRiffReplyAuthor,
  privateRiffShareAllCount,
  privateRiffSourceTitle,
} from './privateRiff';

type Props = {
  visible: boolean;
  riff: PrivateRiffBinding | null;
  reply: ScoutMessage | null;
  messages: readonly ScoutMessage[];
  publishing: 'all' | 'reply' | null;
  error?: string;
  onClose: () => void;
  onSubmit: (mode: 'all' | 'reply') => void;
};

export function PrivateRiffShareSheet({ visible, riff, reply, messages, publishing, error, onClose, onSubmit }: Props) {
  if (!riff || !reply) return null;
  const source = privateRiffSourceTitle(riff);
  const replyAuthor = privateRiffReplyAuthor(reply);
  const turnCount = privateRiffShareAllCount(messages);
  const busy = Boolean(publishing);
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.safe} edges={['top', 'bottom', 'left', 'right']}>
        <View style={styles.header}>
          <View style={styles.heading}>
            <Text style={styles.eyebrow}>PRIVATE RIFF</Text>
            <Text accessibilityRole="header" style={styles.title}>Share to {source}</Text>
          </View>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Close share options"
            accessibilityState={{ disabled: busy }}
            disabled={busy}
            hitSlop={8}
            onPress={onClose}
            style={({ pressed }) => [styles.close, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" size={16} tintColor={colors.text2} />
          </Pressable>
        </View>
        <ScrollView contentInsetAdjustmentBehavior="automatic" contentContainerStyle={styles.content}>
          <View style={styles.boundary}>
            <SymbolView name="guitars.fill" size={18} tintColor={colors.emberText} />
            <Text style={styles.boundaryText}>Choose the whole conversation or only this reply. Authors stay attached to what they said.</Text>
          </View>

          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Share all ${turnCount} Riff messages to ${source}`}
            accessibilityHint="Publishes the initiating message as the channel root and every later turn as replies, preserving each author."
            accessibilityState={{ disabled: busy || turnCount === 0 }}
            disabled={busy || turnCount === 0}
            onPress={() => onSubmit('all')}
            style={({ pressed }) => [styles.choice, pressed && styles.pressed, (busy || turnCount === 0) && styles.disabled]}
          >
            <View style={styles.choiceIcon}>
              {publishing === 'all' ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="text.bubble.fill" size={19} tintColor={colors.emberText} />}
            </View>
            <View style={styles.choiceCopy}>
              <Text style={styles.choiceTitle}>{publishing === 'all' ? 'Sharing all…' : `Share all to ${source}`}</Text>
              <Text style={styles.choiceBody}>Your first message becomes the channel post. The next {Math.max(0, turnCount - 1)} {turnCount - 1 === 1 ? 'turn follows' : 'turns follow'} as replies, under each author’s name.</Text>
            </View>
            <SymbolView name="chevron.right" size={14} tintColor={colors.text3} />
          </Pressable>

          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Share this reply by ${replyAuthor} to ${source}`}
            accessibilityHint="Publishes only this reply under its server-stamped author."
            accessibilityState={{ disabled: busy }}
            disabled={busy}
            onPress={() => onSubmit('reply')}
            style={({ pressed }) => [styles.choice, pressed && styles.pressed, busy && styles.disabled]}
          >
            <View style={styles.choiceIcon}>
              {publishing === 'reply' ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="arrowshape.turn.up.right.fill" size={19} tintColor={colors.emberText} />}
            </View>
            <View style={styles.choiceCopy}>
              <Text style={styles.choiceTitle}>{publishing === 'reply' ? 'Sharing reply…' : `Share this reply to ${source}`}</Text>
              <Text style={styles.choiceBody}>Only this reply from {replyAuthor} is published. The rest of the Riff stays private.</Text>
            </View>
            <SymbolView name="chevron.right" size={14} tintColor={colors.text3} />
          </Pressable>

          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
        </ScrollView>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  header: { minHeight: 62, flexDirection: 'row', alignItems: 'center', paddingHorizontal: space[4], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  heading: { flex: 1, minWidth: 0 },
  eyebrow: { ...type.label, color: colors.emberText },
  title: { ...type.headline, color: colors.text1 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  content: { gap: space[3], padding: space[5] },
  boundary: { minHeight: 56, flexDirection: 'row', alignItems: 'center', gap: space[3], marginBottom: space[2], padding: space[4], borderRadius: radius.lg, backgroundColor: colors.emberSoft },
  boundaryText: { ...type.bodySm, flex: 1, color: colors.text2 },
  choice: { minHeight: 96, flexDirection: 'row', alignItems: 'center', gap: space[3], padding: space[4], borderRadius: radius.xl, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface1 },
  choiceIcon: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.emberSoft },
  choiceCopy: { flex: 1, minWidth: 0, gap: space[1] },
  choiceTitle: { ...type.bodyMedium, color: colors.text1 },
  choiceBody: { ...type.bodySm, color: colors.text2 },
  error: { ...type.bodySm, color: colors.danger, paddingTop: space[2] },
  pressed: { opacity: 0.8, transform: [{ scale: 0.98 }] },
  disabled: { opacity: 0.38 },
});
