import React from 'react';
import { Pressable, ScrollView, StyleSheet, Text } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import type { ThreadDigestResponse } from '../api/types';
import { colors, radius, space, type } from '../theme/tokens';

/**
 * The deposit rail — design §12 of docs/plans/the-table-design.md.
 *
 * What this conversation produced, pinned to the thread that produced it.
 *
 * It lives in the THREAD, not in a separate Work tab, and that placement IS the
 * feature: "I know we shared it here somewhere" has to be answered where the
 * asking happens. A Work tab you have to remember to visit is a Work tab you
 * don't visit.
 *
 * Absent when empty. A strip that narrates its own emptiness is chrome.
 */

export type DepositRailProps = {
  deposits: ThreadDigestResponse['deposits'] | null;
  onOpenMessage: (messageId: string) => void;
};

function iconFor(mime: string | undefined): SFSymbol {
  const value = String(mime ?? '').toLowerCase();
  if (value.startsWith('image/')) return 'photo';
  if (value.includes('pdf')) return 'doc.richtext';
  return 'doc';
}

export function DepositRail({ deposits, onOpenMessage }: DepositRailProps) {
  const files = deposits?.files ?? [];
  // Ordinary links belong to the message that carried them. Repeating them in
  // this rail detached the URL from its context and, on iOS, allowed the
  // horizontal ScrollView to claim the whole remaining height. Only durable
  // file deposits stay pinned here; links now render inline with rich previews.
  if (files.length === 0) return null;

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerStyle={styles.rail}
      style={styles.scroller}
      accessibilityLabel="Shared in this thread"
    >
      {files.map((file) => (
        <Pressable
          key={`file-${file.messageId}-${file.name}`}
          accessibilityRole="button"
          accessibilityLabel={`${file.name}, shared by ${file.author || 'someone'}`}
          // Tapping scrolls to where it was shared rather than opening it
          // blind — the context of a file in a chat is usually the point.
          onPress={() => onOpenMessage(file.messageId)}
          style={({ pressed }) => [styles.chip, pressed && styles.pressed]}
        >
          <SymbolView name={iconFor(file.mime)} tintColor={colors.text2} size={13} />
          <Text style={styles.chipText} numberOfLines={1}>
            {file.name}
          </Text>
        </Pressable>
      ))}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  scroller: { flexGrow: 0, maxHeight: 48 },
  rail: {
    flexDirection: 'row',
    gap: space[2],
    paddingHorizontal: space[4],
    paddingBottom: space[3],
  },
  chip: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    paddingVertical: 7,
    borderRadius: radius.full,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    // Long filenames must not push the rail into a single giant chip.
    maxWidth: 190,
  },
  pressed: { opacity: 0.6 },
  chipText: {
    ...type.caption,
    color: colors.text1,
    flexShrink: 1,
  },
});
