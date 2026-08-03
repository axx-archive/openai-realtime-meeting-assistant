import React, { useMemo } from 'react';
import { ActivityIndicator, Animated, Pressable, StyleSheet, Text, View } from 'react-native';
import { Image } from 'expo-image';
import { SymbolView } from 'expo-symbols';
import * as Linking from 'expo-linking';
import { useMappingHelper } from '@shopify/flash-list';

import type { ScoutFileAttachment, ScoutMessage } from '../api/types';
import { authenticatedFileHeaders, authenticatedFileUrl } from '../files/fileActions';
import { colors, radius, shadow, space, type } from '../theme/tokens';
import { LinkPreviewCard } from './LinkPreviewCard';
import { ScoutRichText } from './ScoutRichText';
import { ChatAvatar } from './ChatAvatar';
import { messageLongPressDelayMs } from './messageGestures';
import { scoutReplyLifecyclePresentation } from './scoutReplyLifecycle';
import {
  extractHttpUrls,
  groupMessageReactions,
  parseMessageTextSegments,
} from './messagePresentation';

export type MessageBubbleProps = {
  message: ScoutMessage;
  own: boolean;
  showAuthor: boolean;
  showAvatar?: boolean;
  avatarDataURL?: string;
  sessionToken: string;
  viewerEmail: string;
  timestampReveal: Animated.Value;
  onOpenSource?: (messageId: string) => void;
  onOpenReplySource?: (messageId: string) => void;
  onLongPress?: (message: ScoutMessage, own: boolean) => void;
  onOpenAttachment?: (file: ScoutFileAttachment) => void;
  onToggleReaction?: (message: ScoutMessage, emoji: string, active: boolean) => void;
  onRetryReply?: (message: ScoutMessage) => void;
  onOpenLongMessage?: (text: string, authorName: string, scout: boolean) => void;
  retryingReply?: boolean;
};

function isScout(message: ScoutMessage): boolean {
  const role = String(message.role ?? '').toLowerCase();
  return role === 'assistant' || role === 'scout';
}

function bodyOf(message: ScoutMessage): string {
  return String(message.text ?? message.content ?? '').trim();
}

function timeOf(message: ScoutMessage): string {
  if (!message.createdAt) return '';
  const at = new Date(String(message.createdAt));
  if (Number.isNaN(at.getTime())) return '';
  return at.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

function attachmentLabel(file: ScoutFileAttachment): string {
  const size = Number(file.size ?? 0);
  const detail = size > 0 ? ` · ${size < 1_048_576 ? `${Math.max(1, Math.round(size / 1024))} KB` : `${(size / 1_048_576).toFixed(1)} MB`}` : '';
  return `${file.name}${detail}`;
}

function shortenedMessage(value: string, maxCharacters: number): string {
  if (value.length <= maxCharacters) return value;
  const slice = value.slice(0, maxCharacters).replace(/\s+\S*$/u, '').trimEnd();
  return `${slice || value.slice(0, maxCharacters).trimEnd()}…`;
}

export const MessageBubble = React.memo(function MessageBubble({
  message,
  own,
  showAuthor,
  showAvatar = false,
  avatarDataURL,
  sessionToken,
  viewerEmail,
  timestampReveal,
  onOpenSource,
  onOpenReplySource,
  onLongPress,
  onOpenAttachment,
  onToggleReaction,
  onRetryReply,
  onOpenLongMessage,
  retryingReply = false,
}: MessageBubbleProps) {
  const lifecycle = scoutReplyLifecyclePresentation(message);
  const rawBody = bodyOf(message);
  const body = rawBody || (!lifecycle?.active ? lifecycle?.fallbackText ?? '' : '');
  const { getMappingKey } = useMappingHelper();
  const files = Array.isArray(message.files) ? message.files : [];
  const scout = isScout(message);
  const replyTo = message.replyTo;
  const viaScout = String(message.postedOnBehalfOf ?? '').trim() !== '';
  const sources = Array.isArray(message.sources) ? message.sources : [];
  const longMessage = body.length > 700 || body.split('\n').length > 12;
  const authorName = scout ? 'Scout' : own ? 'You' : String(message.authorName ?? 'Someone');
  const inlineBody = longMessage && !scout ? shortenedMessage(body, 560) : body;
  const segments = useMemo(() => parseMessageTextSegments(inlineBody), [inlineBody]);
  const urls = useMemo(() => extractHttpUrls(body), [body]);
  const firstURL = urls[0]?.url ?? '';
  const linkOnly = urls.length === 1 && body.trim() === firstURL;
  const standaloneLinkPreview = linkOnly && !replyTo;
  const attachmentOnly = !body && !replyTo && files.length > 0;
  const reactions = useMemo(
    () => groupMessageReactions(message.reactions, viewerEmail),
    [message.reactions, viewerEmail],
  );
  const timeLabel = useMemo(() => timeOf(message), [message.createdAt]);
  const translated = useMemo(() => ({
    transform: [{ translateX: timestampReveal.interpolate({ inputRange: [0, 1], outputRange: [0, -68] }) }],
  }), [timestampReveal]);
  const timestampOpacity = useMemo(() => ({ opacity: timestampReveal }), [timestampReveal]);

  if (!body && files.length === 0 && !lifecycle) return null;

  return (
    <View style={[styles.row, own && styles.rowOwn, showAuthor && styles.rowNewAuthor, reactions.length > 0 && styles.rowWithReactions]}>
      <Animated.View pointerEvents="none" style={[styles.timestampWrap, timestampOpacity]}>
        {message.editedAt ? <Text style={styles.editedLabel}>Edited</Text> : null}
        <Text style={styles.time}>{timeLabel}</Text>
      </Animated.View>
      {!own && !scout ? (
        <View style={styles.avatarSlot}>
          {showAvatar ? (
            <ChatAvatar name={String(message.authorName ?? 'Someone')} avatarDataURL={avatarDataURL} size={28} />
          ) : null}
        </View>
      ) : null}
      <Animated.View style={[styles.stack, own && styles.stackOwn, translated]}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={`${own ? 'You' : String(message.authorName ?? (scout ? 'Scout' : 'Someone'))}: ${body || lifecycle?.label || `${files.length} attachment${files.length === 1 ? '' : 's'}`}. ${message.editedAt ? 'Edited. ' : ''}${timeLabel}`}
          accessibilityHint={longMessage ? 'Opens the full message. Touch and hold for message actions' : 'Touch and hold for message actions'}
          accessibilityActions={[{ name: 'longpress', label: 'Show message actions' }]}
          delayLongPress={messageLongPressDelayMs}
          onAccessibilityAction={(event) => {
            if (event.nativeEvent.actionName === 'longpress') onLongPress?.(message, own);
          }}
          onPress={longMessage ? () => onOpenLongMessage?.(body, authorName, scout) : undefined}
          onLongPress={() => onLongPress?.(message, own)}
          style={[
            styles.bubble,
            own ? styles.bubbleOwn : styles.bubbleOther,
            scout && styles.bubbleScout,
            standaloneLinkPreview && styles.bubbleLinkOnly,
            attachmentOnly && styles.bubbleAttachmentOnly,
          ]}
        >
          {showAuthor && !own ? (
            <Text style={[styles.author, scout && styles.authorScout]}>
              {scout ? 'Scout' : String(message.authorName ?? 'Someone')}
            </Text>
          ) : null}

          {viaScout ? (
            <View style={[styles.viaChip, own && styles.viaChipOwn]}>
              <Text style={[styles.viaText, own && styles.viaTextOwn]}>via Scout</Text>
            </View>
          ) : null}

          {replyTo?.messageId ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`Replying to ${replyTo.authorName}: ${replyTo.text}`}
              accessibilityHint="Jumps to the original message"
              onPress={() => onOpenReplySource?.(replyTo.messageId)}
              onLongPress={() => onLongPress?.(message, own)}
              delayLongPress={messageLongPressDelayMs}
              style={({ pressed }) => [styles.replyContext, pressed && styles.replyContextPressed]}
            >
              <View style={[styles.replyLine, own && styles.replyLineOwn]} />
              <View style={styles.replyCopy}>
                <Text numberOfLines={1} style={[styles.replyAuthor, own && styles.replyAuthorOwn]}>{replyTo.authorName}</Text>
                <Text numberOfLines={2} style={[styles.replyText, own && styles.replyTextOwn]}>{replyTo.text}</Text>
              </View>
            </Pressable>
          ) : null}

          {lifecycle?.active ? (
            <View accessibilityLiveRegion="polite" style={styles.lifecycleActive}>
              <ActivityIndicator color={colors.emberText} size="small" />
              <Text style={styles.lifecycleActiveText}>{lifecycle.label}</Text>
            </View>
          ) : null}

          {body && lifecycle?.state === 'canceled' ? (
            <Text style={styles.lifecycleCanceled}>{body}</Text>
          ) : body && !linkOnly && scout ? (
            <ScoutRichText text={body} maxCharacters={longMessage ? 560 : undefined} />
          ) : body && !linkOnly ? (
            <Text style={[styles.body, own && styles.bodyOwn]}>
              {segments.map((segment, index) => {
                const mappingKey = getMappingKey(`${segment.kind}-${segment.text}`, index);
                if (segment.kind === 'text') return <React.Fragment key={mappingKey}>{segment.text}</React.Fragment>;
                if (segment.kind === 'link') {
                  return (
                    <Text
                      key={mappingKey}
                      accessibilityRole="link"
                      onPress={() => void Linking.openURL(segment.url).catch(() => undefined)}
                      style={[styles.link, own && styles.linkOwn]}
                    >
                      {segment.text}
                    </Text>
                  );
                }
                return (
                  <Text
                    key={mappingKey}
                    accessibilityLabel={`Mention ${segment.text.replace(/^@/, '')}`}
                    style={[
                      styles.mention,
                      own && styles.mentionOwn,
                      segment.scout && styles.mentionScout,
                      segment.scout && own && styles.mentionScoutOwn,
                    ]}
                  >
                    {segment.text.replace(/^@/, '')}
                  </Text>
                );
              })}
            </Text>
          ) : null}

          {lifecycle?.state === 'failed' && lifecycle.retryable && onRetryReply ? (
            <Pressable
              accessibilityLabel="Retry Scout reply"
              accessibilityRole="button"
              accessibilityState={{ disabled: retryingReply }}
              disabled={retryingReply}
              onPress={() => onRetryReply(message)}
              style={({ pressed }) => [styles.lifecycleRetry, pressed && styles.lifecycleRetryPressed]}
            >
              {retryingReply ? <ActivityIndicator color={colors.emberText} size="small" /> : null}
              <Text style={styles.lifecycleRetryText}>{retryingReply ? 'Retrying' : 'Retry'}</Text>
            </Pressable>
          ) : null}

          {longMessage ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={scout ? 'Read full Scout response' : 'Read full message'}
              onPress={() => onOpenLongMessage?.(body, authorName, scout)}
              style={({ pressed }) => [styles.readMore, own && styles.readMoreOwn, pressed && styles.readMorePressed]}
            >
              <Text style={[styles.readMoreText, own && styles.readMoreTextOwn]}>{scout ? 'Read full response' : 'Read full message'}</Text>
              <SymbolView name="arrow.up.left.and.arrow.down.right" size={12} tintColor={own ? colors.onAccent : colors.text2} />
            </Pressable>
          ) : null}

          {files.map((file, index) => {
            const imageAttachment = file.mime?.toLowerCase().startsWith('image/');
            const image = imageAttachment && authenticatedFileUrl(file);
            return (
              <Pressable
                key={getMappingKey(`${file.ref}-${file.name}`, index)}
                accessibilityRole="button"
                accessibilityLabel={`Open ${file.name}`}
                onPress={() => onOpenAttachment?.(file)}
                onLongPress={() => onLongPress?.(message, own)}
                delayLongPress={messageLongPressDelayMs}
                style={({ pressed }) => [
                  styles.attachment,
                  image && styles.attachmentMedia,
                  image && styles.attachmentVisual,
                  pressed && styles.attachmentPressed,
                ]}
              >
                {image ? (
                  <>
                    <Image
                      source={{ uri: image, headers: authenticatedFileHeaders(sessionToken, file.mime) }}
                      cachePolicy="memory-disk"
                      contentFit="cover"
                      enforceEarlyResizing
                      recyclingKey={file.ref}
                      style={styles.attachmentImage}
                    />
                  </>
                ) : (
                  <>
                    <View style={styles.fileIcon}>
                      <SymbolView name="doc.richtext" tintColor={colors.text2} size={20} />
                    </View>
                    <Text numberOfLines={2} style={styles.attachmentText}>{attachmentLabel(file)}</Text>
                    <SymbolView name="chevron.right" tintColor={colors.text3} size={12} />
                  </>
                )}
              </Pressable>
            );
          })}

          {firstURL ? (
            <LinkPreviewCard
              url={firstURL}
              sessionToken={sessionToken}
              own={own}
              seamless={standaloneLinkPreview}
              onLongPress={() => onLongPress?.(message, own)}
            />
          ) : null}
        </Pressable>

        {reactions.length > 0 ? (
          <View style={[styles.reactions, own ? styles.reactionsOwn : styles.reactionsOther]}>
            {reactions.map((reaction, index) => (
              <Pressable
                key={getMappingKey(reaction.emoji, index)}
                accessibilityRole="button"
                accessibilityLabel={`${reaction.emoji}, ${reaction.count} reaction${reaction.count === 1 ? '' : 's'}`}
                onPress={() => onToggleReaction?.(message, reaction.emoji, !reaction.reactedByViewer)}
                hitSlop={{ top: 3, bottom: 3 }}
                style={({ pressed }) => [styles.reactionChip, reaction.reactedByViewer && styles.reactionChipOwn, pressed && styles.reactionPressed]}
              >
                <Text style={styles.reactionEmoji}>{reaction.emoji}</Text>
              </Pressable>
            ))}
          </View>
        ) : null}

        {scout && sources.length > 0 ? (
          <View style={styles.sources}>
            {sources.map((source, index) => (
              <Pressable
                key={getMappingKey(source.messageId, index)}
                accessibilityRole="button"
                accessibilityLabel={`Source: ${source.author || 'a message'} — ${source.quote}`}
                accessibilityHint="Scrolls to the source message"
                onPress={() => onOpenSource?.(source.messageId)}
                style={({ pressed }) => [styles.sourceChip, pressed && styles.sourcePressed]}
              >
                <SymbolView name="quote.opening" tintColor={colors.emberText} size={10} />
                <Text style={styles.sourceText} numberOfLines={1}>{source.author || 'message'}</Text>
              </Pressable>
            ))}
          </View>
        ) : null}
      </Animated.View>
    </View>
  );
});

const styles = StyleSheet.create({
  row: { flexDirection: 'row', paddingHorizontal: space[4], marginBottom: 3 },
  rowNewAuthor: { marginTop: space[3] },
  rowWithReactions: { marginTop: space[5] },
  rowOwn: { justifyContent: 'flex-end' },
  timestampWrap: { position: 'absolute', top: 0, right: space[4], bottom: 0, justifyContent: 'center' },
  avatarSlot: { width: 34, alignSelf: 'stretch', alignItems: 'flex-start', justifyContent: 'flex-end', paddingBottom: 1 },
  time: { fontSize: 11, lineHeight: 13, fontVariant: ['tabular-nums'], color: colors.text3, textAlign: 'right' },
  editedLabel: { fontSize: 10, lineHeight: 12, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', color: colors.text3, textAlign: 'right' },
  stack: { maxWidth: '82%', alignItems: 'flex-start' },
  stackOwn: { alignItems: 'flex-end' },
  bubble: { paddingHorizontal: space[4], paddingVertical: 10, borderRadius: radius.lg, gap: 2 },
  bubbleOther: { backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderBottomLeftRadius: radius.sm },
  bubbleOwn: { backgroundColor: colors.accent, borderBottomRightRadius: radius.sm },
  bubbleScout: { backgroundColor: colors.surface1, borderColor: colors.ember },
  bubbleLinkOnly: { overflow: 'visible', paddingHorizontal: 0, paddingVertical: 0, borderWidth: 0, backgroundColor: 'transparent' },
  bubbleAttachmentOnly: { overflow: 'visible', paddingHorizontal: 0, paddingVertical: 0, borderWidth: 0, backgroundColor: 'transparent' },
  author: { fontSize: 13, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', letterSpacing: -0.05, lineHeight: 17, color: colors.text2, marginBottom: 3 },
  authorScout: { color: colors.emberText },
  viaChip: { alignSelf: 'flex-start', paddingHorizontal: 7, paddingVertical: 2, borderRadius: radius.full, backgroundColor: colors.emberSoft, marginBottom: 4 },
  viaChipOwn: { backgroundColor: 'rgba(0,0,0,0.08)' },
  viaText: { fontSize: 10, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', letterSpacing: 0.3, color: colors.emberText },
  viaTextOwn: { color: colors.onAccent },
  replyContext: { minWidth: 176, maxWidth: 270, flexDirection: 'row', gap: space[2], paddingVertical: 5, marginBottom: 4 },
  replyContextPressed: { opacity: 0.7 },
  replyLine: { width: 3, alignSelf: 'stretch', borderRadius: radius.full, backgroundColor: colors.info },
  replyLineOwn: { backgroundColor: colors.onAccent },
  replyCopy: { flex: 1 },
  replyAuthor: { ...type.captionMedium, color: colors.info },
  replyAuthorOwn: { color: colors.onAccent },
  replyText: { ...type.caption, color: colors.text2 },
  replyTextOwn: { color: colors.onAccent, opacity: 0.68 },
  body: { ...type.body, color: colors.text1 },
  bodyOwn: { color: colors.onAccent },
  lifecycleActive: { minHeight: 28, flexDirection: 'row', alignItems: 'center', gap: space[2] },
  lifecycleActiveText: { ...type.bodySm, color: colors.text2 },
  lifecycleCanceled: { ...type.bodySm, color: colors.text3 },
  lifecycleRetry: { minHeight: 34, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, marginTop: space[2], paddingHorizontal: 10, borderRadius: radius.full, backgroundColor: colors.emberSoft },
  lifecycleRetryPressed: { opacity: 0.68, transform: [{ scale: 0.98 }] },
  lifecycleRetryText: { ...type.captionMedium, color: colors.emberText },
  link: { color: colors.info, textDecorationLine: 'underline' },
  linkOwn: { color: colors.onAccent, textDecorationColor: 'rgba(14,14,16,0.45)' },
  mention: { ...type.bodyMedium, color: colors.info },
  mentionOwn: { color: colors.onAccent, textDecorationLine: 'underline' },
  mentionScout: { color: colors.emberText },
  mentionScoutOwn: { color: colors.onAccentEmber },
  readMore: { minHeight: 34, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, marginTop: space[3], paddingHorizontal: 10, borderRadius: radius.full, backgroundColor: colors.surface3 },
  readMoreOwn: { backgroundColor: 'rgba(255,255,255,0.14)' },
  readMorePressed: { opacity: 0.72, transform: [{ scale: 0.98 }] },
  readMoreText: { ...type.captionMedium, color: colors.text1 },
  readMoreTextOwn: { color: colors.onAccent },
  attachment: { minWidth: 210, maxWidth: 280, minHeight: 58, flexDirection: 'row', alignItems: 'center', gap: space[2], marginTop: space[2], paddingRight: space[3], overflow: 'hidden', borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface1 },
  attachmentMedia: { width: 248, minHeight: 0, flexDirection: 'column', alignItems: 'stretch', gap: 0, paddingRight: 0, backgroundColor: colors.surface1 },
  attachmentVisual: { marginTop: 0, borderWidth: 0, borderRadius: radius.lg, backgroundColor: 'transparent' },
  attachmentPressed: { opacity: 0.76, transform: [{ scale: 0.98 }] },
  attachmentImage: { width: '100%', height: 168, backgroundColor: colors.surface3 },
  fileIcon: { width: 42, height: 42, marginLeft: 7, alignItems: 'center', justifyContent: 'center', borderRadius: radius.sm, backgroundColor: colors.surface3 },
  attachmentText: { ...type.captionMedium, flex: 1, color: colors.text1 },
  reactions: { position: 'absolute', top: -17, zIndex: 2, flexDirection: 'row', gap: 5 },
  reactionsOwn: { left: -8 },
  reactionsOther: { right: -8 },
  reactionChip: { ...shadow[1], width: 34, height: 34, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.glassBorder, backgroundColor: colors.glassPanel },
  reactionChipOwn: { backgroundColor: colors.surface3, transform: [{ scale: 1.02 }] },
  reactionPressed: { transform: [{ scale: 0.96 }], opacity: 0.78 },
  reactionEmoji: { fontSize: 17, lineHeight: 22 },
  sources: { flexDirection: 'row', flexWrap: 'wrap', gap: 6, marginTop: 5, marginLeft: space[1] },
  sourceChip: { flexDirection: 'row', alignItems: 'center', gap: 4, paddingHorizontal: 8, paddingVertical: 3, borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.ember, backgroundColor: colors.emberSoft, maxWidth: 150 },
  sourcePressed: { opacity: 0.6 },
  sourceText: { fontSize: 11, fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500', color: colors.emberText, flexShrink: 1 },
});
