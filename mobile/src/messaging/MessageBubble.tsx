import React, { useMemo } from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';

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
  /** Scrolls to a cited message when a source chip is tapped. */
  onOpenSource?: (messageId: string) => void;
	/** Opens an authenticated attachment from desktop or mobile. */
	onOpenAttachment?: (file: NonNullable<ScoutMessage['files']>[number]) => void;
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
  onOpenSource,
	onOpenAttachment,
}: MessageBubbleProps) {
  const body = bodyOf(message);
  const scout = isScout(message);
  const viaScout = String(message.postedOnBehalfOf ?? '').trim() !== '';
  const sources = Array.isArray(message.sources) ? message.sources : [];
	const files = Array.isArray(message.files) ? message.files : [];
  // Memoized per message: parsing every message on every list render is the
  // performance trap this list exists to avoid (§15).
  const segments = useMemo(() => parseMentions(body), [body]);

	if (!body && files.length === 0) return null;

  return (
    // Grouping is spacing, not just a hidden name. Consecutive messages from
    // one person are one utterance and sit tight; a change of author is a turn
    // in the conversation and earns real air. A uniform gap makes a thread read
    // as a list of records rather than as people talking.
    <View style={[styles.row, own && styles.rowOwn, showAuthor && styles.rowNewAuthor]}>
      {/* Column so the source chips stack BENEATH the bubble. In the row
          container they would sit beside it and push the bubble narrow. */}
      <View style={[styles.stack, own && styles.stackOwn]}>
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

        {/* The disclosure chip. `postedOnBehalfOf` is stamped server-side
            UNCONDITIONALLY from the authenticated requester whenever Scout
            posts as a user, precisely so Scout can never silently impersonate
            anyone — and this bubble ignored it until now. Rendering it is a
            disclosure requirement, not decoration, and it matters more the
            moment a team's primary conversation lives in this surface. */}
        {viaScout ? (
          <View style={[styles.viaChip, own && styles.viaChipOwn]}>
            <Text style={[styles.viaText, own && styles.viaTextOwn]}>via Scout</Text>
          </View>
        ) : null}

		{body ? <Text style={[styles.body, own && styles.bodyOwn]}>
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
		</Text> : null}

		{files.length > 0 ? (
			<View style={styles.attachments}>
				{files.map((file) => (
					<Pressable
						key={`${file.ref}:${file.name}`}
						accessibilityRole="button"
						accessibilityLabel={`Open attachment ${file.name}`}
						onPress={() => onOpenAttachment?.(file)}
						style={({ pressed }) => [styles.attachment, own && styles.attachmentOwn, pressed && styles.sourcePressed]}
					>
						<SymbolView name="paperclip" tintColor={own ? colors.onAccent : colors.text2} size={14} />
						<Text style={[styles.attachmentText, own && styles.bodyOwn]} numberOfLines={1}>{file.name}</Text>
					</Pressable>
				))}
			</View>
		) : null}

        <Text style={[styles.time, own && styles.timeOwn]}>{timeOf(message)}</Text>
      </View>

      {/* Ask-the-thread citations — design §10.
          Only ever present on a Scout answer that PROVABLY quotes a message in
          this thread. An answer that quotes nothing shows nothing, which is the
          design's explicit requirement: no chips beats unearned authority. */}
      {scout && sources.length > 0 ? (
        <View style={styles.sources}>
          {sources.map((source) => (
            <Pressable
              key={source.messageId}
              accessibilityRole="button"
              accessibilityLabel={`Source: ${source.author || 'a message'} — ${source.quote}`}
              accessibilityHint="Scrolls to the message this answer draws on."
              onPress={() => onOpenSource?.(source.messageId)}
              style={({ pressed }) => [styles.sourceChip, pressed && styles.sourcePressed]}
            >
              <SymbolView name="quote.opening" tintColor={colors.emberText} size={10} />
              <Text style={styles.sourceText} numberOfLines={1}>
                {source.author || 'message'}
              </Text>
            </Pressable>
          ))}
        </View>
      ) : null}
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
  stack: {
    // maxWidth lives here now so the bubble AND its chips share one measure.
    maxWidth: '82%',
    alignItems: 'flex-start',
  },
  stackOwn: { alignItems: 'flex-end' },
  bubble: {
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
  viaChip: {
    alignSelf: 'flex-start',
    paddingHorizontal: 7,
    paddingVertical: 2,
    borderRadius: radius.full,
    backgroundColor: colors.emberSoft,
    marginBottom: 4,
  },
  viaChipOwn: { backgroundColor: 'rgba(255,255,255,0.18)' },
  viaText: {
    fontSize: 10,
    fontWeight: '600',
    letterSpacing: 0.3,
    color: colors.emberText,
  },
  viaTextOwn: { color: colors.onAccent },
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
	attachments: { gap: space[1], marginTop: space[1] },
	attachment: {
		minHeight: 44,
		maxWidth: 240,
		paddingHorizontal: space[3],
		borderRadius: radius.md,
		flexDirection: 'row',
		alignItems: 'center',
		gap: space[2],
		backgroundColor: colors.surface3,
	},
	attachmentOwn: { backgroundColor: 'rgba(255,255,255,0.16)' },
	attachmentText: { ...type.captionMedium, color: colors.text1, flexShrink: 1 },
  sources: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 6,
    marginTop: 5,
    marginLeft: space[1],
  },
  sourceChip: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: radius.full,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.ember,
    backgroundColor: colors.emberSoft,
    maxWidth: 150,
  },
  sourcePressed: { opacity: 0.6 },
  sourceText: {
    fontSize: 11,
    fontWeight: '500',
    color: colors.emberText,
    flexShrink: 1,
  },
});
