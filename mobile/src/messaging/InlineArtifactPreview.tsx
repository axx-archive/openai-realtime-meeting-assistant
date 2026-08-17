import React, { useEffect, useState } from 'react';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from 'react-native';
import { WebView } from 'react-native-webview';
import { SymbolView } from 'expo-symbols';
import { api } from '../api/client';
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
  artifactId?: string;
  sessionToken?: string;
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
 * For html_deck: the 16:9 IS the first slide. WebView fills the glass.
 * Artifact HTML is THE document (not nested). No ScoutRichText fallback.
 *
 * For other kinds: displays text preview via ScoutRichText with badge/title.
 * Does NOT dump to LongMessageSheet. Includes Edit/Present actions.
 */
export function InlineArtifactPreview({
  kind,
  title,
  text,
  agentName = 'Scout',
  loading = false,
  artifactId,
  sessionToken,
  onEdit,
  onPresent,
  onExpand,
}: Props) {
  const { width: screenWidth } = useWindowDimensions();
  const [expanded, setExpanded] = useState(false);
  const [deckHtml, setDeckHtml] = useState<string | null>(null);
  const [deckLoading, setDeckLoading] = useState(false);
  const [deckError, setDeckError] = useState(false);
  const isPresentable = kind === 'html_deck';

  // Explicit 16:9 dimensions - not flex leftover
  const containerWidth = Math.min(screenWidth - 48, 440);
  const containerHeight = containerWidth * (9 / 16);

  // Fetch actual deck HTML for real in-thread deck view
  useEffect(() => {
    if (kind !== 'html_deck' || !artifactId || !sessionToken || loading) {
      setDeckHtml(null);
      return;
    }
    let active = true;
    setDeckLoading(true);
    setDeckError(false);
    api.artifact(sessionToken, artifactId)
      .then((response) => {
        if (!active) return;
        const artifact = response.artifacts?.[0];
        const html = String(artifact?.text ?? '').trim();
        if (html && html.includes('<')) {
          setDeckHtml(html);
        } else {
          setDeckError(true);
        }
        setDeckLoading(false);
      })
      .catch(() => {
        if (!active) return;
        setDeckError(true);
        setDeckLoading(false);
      });
    return () => { active = false; };
  }, [kind, artifactId, sessionToken, loading]);

  // html_deck: the 16:9 IS the slide
  if (isPresentable) {
    // Loading state during creation
    if (loading) {
      return (
        <View style={[styles.deckContainer, { width: containerWidth, height: containerHeight }]}>
          <Glass radius={radius.lg} style={styles.deckGlass}>
            <View style={styles.deckLoadingCenter}>
              <ActivityIndicator color={colors.emberText} size="large" />
              <Text style={styles.deckLoadingText}>Creating presentation…</Text>
            </View>
          </Glass>
        </View>
      );
    }

    // Loading deck HTML
    if (deckLoading && artifactId) {
      return (
        <View style={[styles.deckContainer, { width: containerWidth, height: containerHeight }]}>
          <Glass radius={radius.lg} style={styles.deckGlass}>
            <View style={styles.deckLoadingCenter}>
              <ActivityIndicator color={colors.emberText} size="large" />
              <Text style={styles.deckLoadingText}>Loading deck…</Text>
            </View>
          </Glass>
        </View>
      );
    }

    // Error or missing content - no ScoutRichText fallback
    if (deckError || !deckHtml) {
      return (
        <View style={[styles.deckContainer, { width: containerWidth, height: containerHeight }]}>
          <Glass radius={radius.lg} style={styles.deckGlass}>
            <View style={styles.deckLoadingCenter}>
              <SymbolView name="exclamationmark.triangle" size={32} tintColor={colors.text3} />
              <Text style={styles.deckErrorText}>Could not load deck</Text>
              {onPresent ? (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Open in browser"
                  onPress={onPresent}
                  style={({ pressed }) => [styles.deckRetryButton, pressed && styles.deckRetryPressed]}
                >
                  <Text style={styles.deckRetryText}>Open in browser</Text>
                </Pressable>
              ) : null}
            </View>
          </Glass>
        </View>
      );
    }

    // Real deck: WebView fills the glass, artifact HTML is THE document
    return (
      <View style={[styles.deckContainer, { width: containerWidth, height: containerHeight }]}>
        <View style={styles.deckWebViewWrapper}>
          <WebView
            source={{ html: deckHtml }}
            style={styles.deckWebViewFill}
            scrollEnabled={false}
            originWhitelist={['*']}
            javaScriptEnabled
            domStorageEnabled
            showsHorizontalScrollIndicator={false}
            showsVerticalScrollIndicator={false}
            injectedJavaScript={DECK_FIRST_SLIDE_JS}
            onMessage={() => {}}
          />
        </View>
        {/* Floating actions over the slide */}
        <View style={styles.deckOverlayActions}>
          {onEdit ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Edit presentation"
              onPress={onEdit}
              style={({ pressed }) => [styles.deckActionButton, pressed && styles.deckActionPressed]}
            >
              <SymbolView name="pencil" size={14} tintColor={colors.onAccent} />
              <Text style={styles.deckActionText}>Edit</Text>
            </Pressable>
          ) : null}
          {onPresent ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Present"
              onPress={onPresent}
              style={({ pressed }) => [styles.deckActionButton, styles.deckActionPrimary, pressed && styles.deckActionPressed]}
            >
              <SymbolView name="play.fill" size={14} tintColor={colors.onAccent} />
              <Text style={styles.deckActionText}>Present</Text>
            </Pressable>
          ) : null}
        </View>
      </View>
    );
  }

  // Non-deck kinds: badge + title + text preview
  const previewLines = text.split('\n').slice(0, expanded ? undefined : 12);
  const hasMore = text.split('\n').length > 12;

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

/**
 * JS injected into WebView to show only the first slide.
 * Runs after document load to hide subsequent slides.
 */
const DECK_FIRST_SLIDE_JS = `
(function() {
  var slides = document.querySelectorAll('.slide, section, [class*="slide"]');
  for (var i = 1; i < slides.length; i++) {
    slides[i].style.display = 'none';
  }
  document.body.style.overflow = 'hidden';
  true;
})();
`;

const styles = StyleSheet.create({
  // Deck-specific styles (16:9 IS the slide)
  deckContainer: {
    borderRadius: radius.lg,
    overflow: 'hidden',
    backgroundColor: colors.surface1,
  },
  deckGlass: {
    flex: 1,
    width: '100%',
    height: '100%',
  },
  deckWebViewWrapper: {
    flex: 1,
    width: '100%',
    height: '100%',
    borderRadius: radius.lg,
    overflow: 'hidden',
  },
  deckWebViewFill: {
    flex: 1,
    width: '100%',
    height: '100%',
    backgroundColor: 'transparent',
  },
  deckLoadingCenter: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[3],
  },
  deckLoadingText: {
    ...type.body,
    color: colors.emberText,
  },
  deckErrorText: {
    ...type.body,
    color: colors.text3,
  },
  deckRetryButton: {
    marginTop: space[2],
    paddingHorizontal: space[4],
    paddingVertical: space[2],
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
  },
  deckRetryPressed: {
    opacity: 0.7,
  },
  deckRetryText: {
    ...type.captionMedium,
    color: colors.text1,
  },
  deckOverlayActions: {
    position: 'absolute',
    bottom: space[3],
    right: space[3],
    flexDirection: 'row',
    gap: space[2],
  },
  deckActionButton: {
    minHeight: 36,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    backgroundColor: 'rgba(0,0,0,0.6)',
  },
  deckActionPrimary: {
    backgroundColor: colors.accent,
  },
  deckActionPressed: {
    opacity: 0.8,
    transform: [{ scale: 0.98 }],
  },
  deckActionText: {
    ...type.captionMedium,
    color: colors.onAccent,
  },

  // Non-deck styles (badge + title + text)
  container: {
    width: '100%',
    maxWidth: 440,
    aspectRatio: 16 / 9,
    minHeight: 200,
    padding: space[4],
    gap: space[3],
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
  actionPressed: {
    opacity: 0.76,
    transform: [{ scale: 0.98 }],
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
    borderRadius: radius.md,
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
