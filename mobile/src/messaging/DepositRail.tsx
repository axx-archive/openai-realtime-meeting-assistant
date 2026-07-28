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
  onOpenLink: (url: string) => void;
};

function iconFor(mime: string | undefined): SFSymbol {
  const value = String(mime ?? '').toLowerCase();
  if (value.startsWith('image/')) return 'photo';
  if (value.includes('pdf')) return 'doc.richtext';
  return 'doc';
}

export function DepositRail({ deposits, onOpenMessage, onOpenLink }: DepositRailProps) {
  const files = deposits?.files ?? [];
  const links = deposits?.links ?? [];
  if (files.length === 0 && links.length === 0) return null;

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerStyle={styles.rail}
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

      {links.map((link) => (
        <Pressable
          key={`link-${link.url}`}
          accessibilityRole="link"
          accessibilityLabel={`${link.host}, shared by ${link.author || 'someone'}`}
          onPress={() => onOpenLink(link.url)}
          style={({ pressed }) => [styles.chip, pressed && styles.pressed]}
        >
          <SymbolView name="link" tintColor={colors.text2} size={13} />
          {/* The host, not the URL — a full URL truncates to nothing useful
              in a chip this size. */}
          <Text style={styles.chipText} numberOfLines={1}>
            {link.host || link.url}
          </Text>
        </Pressable>
      ))}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
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
