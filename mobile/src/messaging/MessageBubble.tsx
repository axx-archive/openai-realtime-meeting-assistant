import React, { useMemo } from 'react';
import { StyleSheet, Text, View } from 'react-native';
import type { ScoutMessage } from '../api/types';
import { parseMentions } from './mentions';
import { colors, radius, space, type } from '../theme/tokens';

/**
 * A message — design §14.
 *
 * Bubbles are deliberately NOT glass. Glass is a variable backdrop with no
 * contrast guarantee, and this is the text people read all day; the glass law
 * (§7) reserves the material for things floating above the conversation, not the
 * conversation itself.
 *
 * Scout renders in ember because agent work is exactly what ember is earned for.
 */

export type MessageBubbleProps = {
  message: ScoutMessage;
  /** True when the signed-in user wrote it. */
  own: boolean;
  /** False when the previous message shares this author — suppresses the name. */
  showAuthor: boolean;
};

function isScout(message: ScoutMessage): boolean {
  const role = String(message.role ?? '').toLowerCase();
  return role === 'assistant' || role === 'scout';
}

function bodyOf(message: ScoutMessage): string {
  return String(message.text ?? message.content ?? '').trim();
}

function timeOf(message: ScoutMessage): string {
  const raw = message.createdAt;
  if (!raw) return '';
  const at = new Date(String(raw));
  if (Number.isNaN(at.getTime())) return '';
  return at.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

export const MessageBubble = React.memo(function MessageBubble({
  message,
  own,
  showAuthor,
}: MessageBubbleProps) {
  const body = bodyOf(message);
  const scout = isScout(message);
  // Memoized per message: parsing every message on every list render is the
  // performance trap this list exists to avoid (§15).
  const segments = useMemo(() => parseMentions(body), [body]);

  if (!body) return null;

  return (
    // Grouping is spacing, not just a hidden name. Consecutive messages from
    // one person are one utterance and sit tight; a change of author is a turn
    // in the conversation and earns real air. A uniform gap makes a thread read
    // as a list of records rather than as people talking.
    <View style={[styles.row, own && styles.rowOwn, showAuthor && styles.rowNewAuthor]}>
      <View
        style={[
          styles.bubble,
          own ? styles.bubbleOwn : styles.bubbleOther,
          scout && styles.bubbleScout,
        ]}
      >
        {showAuthor && !own ? (
          <Text style={[styles.author, scout && styles.authorScout]}>
            {scout ? 'Scout' : String(message.authorName ?? 'Someone')}
          </Text>
        ) : null}

        <Text style={[styles.body, own && styles.bodyOwn]}>
          {segments.map((segment, index) =>
            segment.kind === 'text' ? (
              segment.text
            ) : (
              <Text
                key={index}
                style={[
                  styles.mention,
                  own && styles.mentionOwn,
                  segment.scout && styles.mentionScout,
                ]}
              >
                {segment.text}
              </Text>
            ),
          )}
        </Text>

        <Text style={[styles.time, own && styles.timeOwn]}>{timeOf(message)}</Text>
      </View>
    </View>
  );
});

const styles = StyleSheet.create({
  row: {
    flexDirection: 'row',
    paddingHorizontal: space[4],
    marginBottom: 3,
  },
  rowNewAuthor: { marginTop: space[3] },
  rowOwn: { justifyContent: 'flex-end' },
  bubble: {
    maxWidth: '82%',
    paddingHorizontal: space[4],
    paddingVertical: space[3],
    borderRadius: radius.lg,
    gap: 2,
  },
  bubbleOther: {
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    borderBottomLeftRadius: radius.sm,
  },
  bubbleOwn: {
    backgroundColor: colors.accent,
    borderBottomRightRadius: radius.sm,
  },
  bubbleScout: {
    backgroundColor: colors.emberSoft,
    borderColor: colors.ember,
  },
  author: {
    fontSize: 13,
    fontWeight: '600',
    letterSpacing: -0.05,
    lineHeight: 17,
    color: colors.text2,
    marginBottom: 3,
  },
  authorScout: { color: colors.ember },
  body: {
    ...type.body,
    color: colors.text1,
  },
  bodyOwn: { color: colors.onAccent },
  mention: {
    ...type.bodyMedium,
    color: colors.info,
  },
  mentionOwn: { color: colors.onAccent, textDecorationLine: 'underline' },
  mentionScout: { color: colors.ember },
  time: {
    // The label token's 0.66 tracking is for uppercase eyebrows; on a timestamp
    // it reads as spaced-out noise. Tabular figures stop the clock jittering as
    // digits change.
    fontSize: 11,
    fontWeight: '400',
    letterSpacing: 0,
    lineHeight: 13,
    fontVariant: ['tabular-nums'],
    color: colors.text3,
    alignSelf: 'flex-end',
    marginTop: 3,
  },
  timeOwn: { color: 'rgba(255,255,255,0.55)' },
});
