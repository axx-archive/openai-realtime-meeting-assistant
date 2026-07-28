import React from 'react';
import { Modal, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';
import type { ThreadDigestResponse } from '../api/types';
import { colors, radius, space, type } from '../theme/tokens';

/**
 * Thread catch-up — design §11 of docs/plans/the-table-design.md.
 *
 * You were away, there are eighty messages, and scrolling all of them is the
 * everything-channel's whole failure mode.
 *
 * Every line here is a VERBATIM slice of a real message, carrying that
 * message's id — the same extractive discipline the room-scoped catch-up
 * enforces, for the same reason: a recap that paraphrases a colleague
 * inaccurately is worse than no recap, because it gets quoted back at them.
 * Nothing on this screen was written by a model.
 */

export type CatchUpSheetProps = {
  visible: boolean;
  catchUp: ThreadDigestResponse['catchUp'] | null;
  onClose: () => void;
  onOpenMessage: (messageId: string) => void;
};

export function CatchUpSheet({ visible, catchUp, onClose, onOpenMessage }: CatchUpSheetProps) {
  const bullets = catchUp?.bullets ?? [];
  const total = catchUp?.totalUnread ?? 0;
  // What the cap left out. Silently truncating would misrepresent the thread
  // as smaller than it is.
  const omitted = Math.max(0, total - bullets.length);

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
        <View style={styles.header}>
          <Text style={styles.title}>Catch up</Text>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Close"
            hitSlop={12}
            onPress={onClose}
            style={styles.close}
          >
            <SymbolView name="xmark" tintColor={colors.text1} size={15} />
          </Pressable>
        </View>

        <ScrollView contentContainerStyle={styles.body}>
          {catchUp?.headline ? <Text style={styles.headline}>{catchUp.headline}</Text> : null}

          {bullets.map((bullet) => (
            <Pressable
              key={bullet.messageId}
              accessibilityRole="button"
              accessibilityLabel={`${bullet.author}: ${bullet.text}`}
              accessibilityHint="Scrolls to this message in the thread."
              onPress={() => onOpenMessage(bullet.messageId)}
              style={({ pressed }) => [styles.bullet, pressed && styles.pressed]}
            >
              <Text style={styles.bulletAuthor}>{bullet.author || 'Someone'}</Text>
              <Text style={styles.bulletText}>{bullet.text}</Text>
            </Pressable>
          ))}

          {bullets.length === 0 ? (
            <Text style={styles.empty}>Nothing substantial while you were away.</Text>
          ) : null}

          {omitted > 0 ? (
            // Named, never silent. A cap that hides what it dropped reads as a
            // complete summary and isn't one.
            <Text style={styles.omitted}>
              {omitted} shorter {omitted === 1 ? 'message' : 'messages'} not shown — scroll the
              thread for everything.
            </Text>
          ) : null}

          <Text style={styles.provenance}>
            Every line above is quoted directly from the thread.
          </Text>
        </ScrollView>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: space[5],
    paddingBottom: space[3],
  },
  title: { ...type.title2, color: colors.text1 },
  close: {
    width: 34,
    height: 34,
    borderRadius: radius.full,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface1,
  },
  body: {
    paddingHorizontal: space[5],
    paddingBottom: space[10],
    gap: space[3],
  },
  headline: {
    ...type.body,
    color: colors.text2,
    marginBottom: space[2],
  },
  bullet: {
    gap: 3,
    padding: space[4],
    borderRadius: radius.lg,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  pressed: { opacity: 0.6 },
  bulletAuthor: {
    fontSize: 13,
    fontWeight: '600',
    color: colors.text2,
  },
  bulletText: {
    ...type.body,
    color: colors.text1,
  },
  empty: {
    ...type.bodySm,
    color: colors.text2,
    paddingVertical: space[4],
  },
  omitted: {
    ...type.caption,
    color: colors.text3,
    marginTop: space[2],
  },
  provenance: {
    ...type.caption,
    color: colors.text3,
    marginTop: space[4],
    fontStyle: 'italic',
  },
});
