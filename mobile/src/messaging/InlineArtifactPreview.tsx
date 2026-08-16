import React, { useState } from 'react';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';
import { Glass } from '../theme/glass';
import { colors, radius, space, type } from '../theme/tokens';
import { ScoutRichText } from './ScoutRichText';

export type InlineArtifactKind = 'html_deck' | 'table' | 'ideation' | 'research' | 'document';

type Props = {
  kind: InlineArtifactKind;
  title: string;
  text: string;
  agentName?: string;
  loading?: boolean;
  onEdit?: () => void;
  onPresent?: () => void;
  onExpand?: () => void;
};

const kindLabel: Record<InlineArtifactKind, string> = {
  html_deck: 'Presentation',
  table: 'Table',
  ideation: 'Ideation',
  research: 'Research',
  document: 'Document',
};

const kindIcon: Record<InlineArtifactKind, string> = {
  html_deck: 'rectangle.on.rectangle',
  table: 'tablecells',
  ideation: 'lightbulb',
  research: 'text.book.closed',
  document: 'doc.text',
};

/**
 * Inline 16:9 glass artifact preview — Scout finishes in-thread.
 *
 * Displays html_deck, table, ideation, and research results inline in the
 * conversation rather than dumping to LongMessageSheet. Includes Edit/Present
 * actions for presentations.
 */
export function InlineArtifactPreview({
  kind,
  title,
  text,
  agentName = 'Scout',
  loading = false,
  onEdit,
  onPresent,
  onExpand,
}: Props) {
  const [expanded, setExpanded] = useState(false);
  const isPresentable = kind === 'html_deck';
  const previewLines = text.split('\n').slice(0, expanded ? undefined : 12);
  const hasMore = text.split('\n').length > 12;

  if (loading) {
    return (
      <Glass radius={radius.lg} style={styles.container}>
        <View style={styles.loadingBody}>
          <ActivityIndicator color={colors.emberText} size="small" />
          <Text style={styles.loadingText}>Loading {kindLabel[kind].toLowerCase()}…</Text>
        </View>
      </Glass>
    );
  }

  return (
    <Glass radius={radius.lg} style={styles.container}>
      <View style={styles.header}>
        <View style={styles.headerLeft}>
          <View style={styles.kindBadge}>
            <SymbolView name={kindIcon[kind] as any} size={12} tintColor={colors.emberText} />
            <Text style={styles.kindText}>{kindLabel[kind]}</Text>
          </View>
          <Text style={styles.byline}>{agentName} · delivered</Text>
        </View>
        {isPresentable ? (
          <View style={styles.actions}>
            {onEdit ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Edit presentation"
                onPress={onEdit}
                style={({ pressed }) => [styles.actionButton, pressed && styles.actionPressed]}
              >
                <SymbolView name="pencil" size={14} tintColor={colors.emberText} />
                <Text style={styles.actionText}>Edit</Text>
              </Pressable>
            ) : null}
            {onPresent ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Present"
                onPress={onPresent}
                style={({ pressed }) => [styles.actionButton, styles.actionPrimary, pressed && styles.actionPressed]}
              >
                <SymbolView name="play.fill" size={14} tintColor={colors.onAccent} />
                <Text style={styles.actionPrimaryText}>Present</Text>
              </Pressable>
            ) : null}
          </View>
        ) : null}
      </View>

      <Text
        accessibilityRole="header"
        style={styles.title}
        numberOfLines={2}
      >
        {title}
      </Text>

      <View style={styles.previewContainer}>
        <ScrollView
          style={styles.preview}
          contentContainerStyle={styles.previewContent}
          scrollEnabled={expanded}
          showsVerticalScrollIndicator={false}
        >
          <ScoutRichText text={previewLines.join('\n')} maxCharacters={expanded ? undefined : 800} />
        </ScrollView>

        {hasMore && !expanded ? (
          <View style={styles.fadeOverlay} pointerEvents="none" />
        ) : null}
      </View>

      {hasMore ? (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={expanded ? 'Show less' : 'Show more'}
          onPress={() => setExpanded(!expanded)}
          style={({ pressed }) => [styles.expandButton, pressed && styles.expandPressed]}
        >
          <Text style={styles.expandText}>{expanded ? 'Show less' : 'Show full result'}</Text>
          <SymbolView
            name={expanded ? 'chevron.up' : 'chevron.down'}
            size={12}
            tintColor={colors.emberText}
          />
        </Pressable>
      ) : null}

      {onExpand ? (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Open in full screen"
          onPress={onExpand}
          style={({ pressed }) => [styles.fullscreenButton, pressed && styles.actionPressed]}
        >
          <SymbolView name="arrow.up.left.and.arrow.down.right" size={12} tintColor={colors.text2} />
          <Text style={styles.fullscreenText}>Full screen</Text>
        </Pressable>
      ) : null}
    </Glass>
  );
}

const styles = StyleSheet.create({
  container: {
    width: '100%',
    maxWidth: 440,
    aspectRatio: 16 / 9,
    minHeight: 200,
    padding: space[4],
    gap: space[3],
  },
  loadingBody: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
  },
  loadingText: {
    ...type.captionMedium,
    color: colors.emberText,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: space[2],
  },
  headerLeft: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    flex: 1,
  },
  kindBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: radius.full,
    backgroundColor: colors.emberSoft,
  },
  kindText: {
    ...type.label,
    color: colors.emberText,
    textTransform: 'uppercase',
    letterSpacing: 0.5,
  },
  byline: {
    ...type.caption,
    color: colors.text3,
    flex: 1,
  },
  actions: {
    flexDirection: 'row',
    gap: space[2],
  },
  actionButton: {
    minHeight: 32,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 5,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
  },
  actionPrimary: {
    backgroundColor: colors.accent,
  },
  actionPressed: {
    opacity: 0.76,
    transform: [{ scale: 0.98 }],
  },
  actionText: {
    ...type.captionMedium,
    color: colors.emberText,
  },
  actionPrimaryText: {
    ...type.captionMedium,
    color: colors.onAccent,
  },
  title: {
    ...type.title2,
    color: colors.text1,
  },
  previewContainer: {
    flex: 1,
    minHeight: 0,
    position: 'relative',
    overflow: 'hidden',
  },
  preview: {
    flex: 1,
  },
  previewContent: {
    paddingRight: space[2],
  },
  fadeOverlay: {
    position: 'absolute',
    left: 0,
    right: 0,
    bottom: 0,
    height: 48,
    backgroundColor: 'transparent',
  },
  expandButton: {
    minHeight: 36,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    backgroundColor: colors.emberSoft,
    alignSelf: 'center',
  },
  expandPressed: {
    opacity: 0.76,
  },
  expandText: {
    ...type.captionMedium,
    color: colors.emberText,
  },
  fullscreenButton: {
    position: 'absolute',
    bottom: space[3],
    right: space[3],
    minHeight: 32,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    paddingHorizontal: space[2],
    borderRadius: radius.sm,
    backgroundColor: colors.surface2,
  },
  fullscreenText: {
    ...type.label,
    color: colors.text2,
  },
});
