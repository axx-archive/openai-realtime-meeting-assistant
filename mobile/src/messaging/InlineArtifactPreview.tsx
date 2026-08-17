import React, { useEffect, useState } from 'react';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
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
 * For html_deck: renders the REAL deck HTML in a WebView (first slide view).
 * For other kinds: displays text preview via ScoutRichText.
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
  const [expanded, setExpanded] = useState(false);
  const [deckHtml, setDeckHtml] = useState<string | null>(null);
  const [deckLoading, setDeckLoading] = useState(false);
  const [deckError, setDeckError] = useState(false);
  const isPresentable = kind === 'html_deck';
  const previewLines = text.split('\n').slice(0, expanded ? undefined : 12);
  const hasMore = text.split('\n').length > 12;

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
        if (html && (html.includes('<') || html.includes('slide'))) {
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

  if (loading || (isPresentable && deckLoading && artifactId)) {
    return (
      <Glass radius={radius.lg} style={styles.container}>
        <View style={styles.loadingBody}>
          <ActivityIndicator color={colors.emberText} size="small" />
          <Text style={styles.loadingText}>
            {loading ? `Creating ${kindLabel[kind].toLowerCase()}…` : 'Loading deck…'}
          </Text>
        </View>
      </Glass>
    );
  }

  // For html_deck with actual HTML content, render in WebView
  const showDeckWebView = isPresentable && deckHtml && !deckError;

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

      {!showDeckWebView ? (
        <Text
          accessibilityRole="header"
          style={styles.title}
          numberOfLines={2}
        >
          {title}
        </Text>
      ) : null}

      <View style={styles.previewContainer}>
        {showDeckWebView ? (
          <WebView
            source={{ html: wrapDeckHtml(deckHtml) }}
            style={styles.deckWebView}
            scrollEnabled={false}
            scalesPageToFit
            originWhitelist={['*']}
            javaScriptEnabled
            domStorageEnabled
            showsHorizontalScrollIndicator={false}
            showsVerticalScrollIndicator={false}
          />
        ) : (
          <ScrollView
            style={styles.preview}
            contentContainerStyle={styles.previewContent}
            scrollEnabled={expanded}
            showsVerticalScrollIndicator={false}
          >
            <ScoutRichText text={previewLines.join('\n')} maxCharacters={expanded ? undefined : 800} />
          </ScrollView>
        )}

        {!showDeckWebView && hasMore && !expanded ? (
          <View style={styles.fadeOverlay} pointerEvents="none" />
        ) : null}
      </View>

      {!showDeckWebView && hasMore ? (
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
 * Wrap deck HTML for inline WebView display.
 * Shows first slide scaled to fit the 16:9 container.
 */
function wrapDeckHtml(html: string): string {
  return `<!DOCTYPE html>
<html>
<head>
  <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    html, body {
      width: 100%;
      height: 100%;
      overflow: hidden;
      background: transparent;
      font-family: -apple-system, system-ui, sans-serif;
    }
    body {
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .deck-container {
      width: 100%;
      height: 100%;
      display: flex;
      align-items: center;
      justify-content: center;
      overflow: hidden;
    }
    .slide {
      transform-origin: center center;
      max-width: 100%;
      max-height: 100%;
    }
    /* Hide all slides except the first */
    .slide:not(:first-child), section:not(:first-child) { display: none; }
  </style>
</head>
<body>
  <div class="deck-container">
    ${html}
  </div>
</body>
</html>`;
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
    borderRadius: radius.md,
  },
  preview: {
    flex: 1,
  },
  deckWebView: {
    flex: 1,
    backgroundColor: 'transparent',
    borderRadius: radius.md,
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
