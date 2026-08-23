import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { Image } from 'expo-image';
import { WebView } from 'react-native-webview';
import { SymbolView } from 'expo-symbols';
import { api } from '../api/client';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';
import { buildApiUrl, buildAuthHeaders } from '../api/requestHelpers';
import { nativeDeckPreviewPath, nativeTextArtifactIsRenderable } from '../artifacts/nativeDeckViewer';
import { Glass } from '../theme/glass';
import { colors, radius, space, type } from '../theme/tokens';
import { ScoutRichText } from './ScoutRichText';
import type { ScoutResultAssetRef, ScoutResultTableRef, ScoutResultWorkbookRef } from '../api/types';
import { authenticatedFileHeaders, authenticatedFileUrl } from '../files/fileActions';
import {
  DECK_PREVIEW_NAVIGATION_JS,
  deckPreviewNavigationCommand,
  deckPreviewNavigationTarget,
  initialDeckPreviewNavigationState,
  parseDeckPreviewNavigationMessage,
  type DeckPreviewNavigationState,
} from './deckPreviewNavigation';

export type InlineArtifactKind = 'html_deck' | 'pdf' | 'image' | 'table' | 'workbook' | 'bundle' | 'file' | 'ideation' | 'research' | 'document' | 'deliverable';

type Props = {
  kind: InlineArtifactKind;
  title: string;
  text: string;
  agentName?: string;
  loading?: boolean;
  needsAttention?: boolean;
  artifactId?: string;
  artifactVersion?: number;
  artifactDigest?: string;
  sessionToken?: string;
  /** Direct HTML content for live html_deck path (bypasses API fetch) */
  htmlContent?: string;
  desktopEditingOnly?: boolean;
  /** Truthful state for historical inline HTML that has no canonical artifact route. */
  previewOnlyLabel?: string;
	assets?: readonly ScoutResultAssetRef[];
	table?: ScoutResultTableRef;
	workbook?: ScoutResultWorkbookRef;
	onOpenAsset?: (asset: ScoutResultAssetRef) => void;
  onEdit?: () => void;
  onPresent?: () => void;
  onExpand?: () => void;
};

const kindLabel: Record<InlineArtifactKind, string> = {
  html_deck: 'Presentation',
	pdf: 'PDF',
	image: 'Image',
  table: 'Table',
	workbook: 'Workbook',
	bundle: 'File bundle',
	file: 'File',
  ideation: 'Ideation',
  research: 'Research',
  document: 'Document',
  deliverable: 'Deliverable',
};

const kindIcon: Record<InlineArtifactKind, string> = {
  html_deck: 'rectangle.on.rectangle',
	pdf: 'doc.richtext',
	image: 'photo',
  table: 'tablecells',
	workbook: 'tablecells.badge.ellipsis',
	bundle: 'shippingbox',
	file: 'doc',
  ideation: 'lightbulb',
  research: 'text.book.closed',
  document: 'doc.text',
  deliverable: 'doc.badge.checkmark',
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
  needsAttention = false,
  artifactId,
  artifactVersion,
  artifactDigest,
  sessionToken,
  htmlContent,
  desktopEditingOnly = false,
  previewOnlyLabel,
	assets = [],
	table,
	workbook,
	onOpenAsset,
  onEdit,
  onPresent,
  onExpand,
}: Props) {
  const [expanded, setExpanded] = useState(false);
  const [deckHtml, setDeckHtml] = useState<string | null>(null);
  const [deckUrl, setDeckUrl] = useState<string | null>(null);
  const [deckLoading, setDeckLoading] = useState(false);
  const [deckError, setDeckError] = useState(false);
  const [deckRetryNonce, setDeckRetryNonce] = useState(0);
  const [deckNavigation, setDeckNavigation] = useState<DeckPreviewNavigationState>(initialDeckPreviewNavigationState);
  const deckNavigationRef = useRef(deckNavigation);
  const deckWebViewRef = useRef<WebView>(null);
  const isPresentable = kind === 'html_deck';

  const updateDeckNavigation = useCallback((next: DeckPreviewNavigationState) => {
    deckNavigationRef.current = next;
    setDeckNavigation(next);
  }, []);

  const resetDeckNavigation = useCallback(() => {
    updateDeckNavigation(initialDeckPreviewNavigationState());
  }, [updateDeckNavigation]);

  // Live path: use htmlContent directly (bypasses API fetch)
  // Work-thread path: fetch from API via artifactId
  useEffect(() => {
    // If htmlContent is provided, use it directly (live path)
    if (kind === 'html_deck' && htmlContent) {
      setDeckHtml(htmlContent);
      setDeckUrl(null);
      setDeckLoading(false);
      setDeckError(false);
      resetDeckNavigation();
      return;
    }

    // Work-thread path: fetch from API
    if (kind !== 'html_deck' || !artifactId || !sessionToken || loading) {
      if (!htmlContent) {
        setDeckHtml(null);
        setDeckUrl(null);
      }
      resetDeckNavigation();
      return;
    }
    let active = true;
    setDeckLoading(true);
    setDeckError(false);
    resetDeckNavigation();
    void (async () => {
      try {
        let path = nativeDeckPreviewPath(artifactId, artifactVersion, artifactDigest);
        // Historical cards predate revision-bound result metadata. Hydrate the
        // current artifact once, then pin the preview to that exact tuple.
        if (!path) {
          const response = await api.artifact(sessionToken, artifactId);
          const artifact = response.artifacts.find((candidate) => candidate.id === artifactId);
          const metadata = artifact?.metadata ?? {};
          const type = String(metadata.type ?? '').trim().toLowerCase();
          if (!artifact || !['html_deck', 'deck', 'presentation', 'slides'].includes(type)) {
            throw new Error('Deck artifact binding is unavailable.');
          }
          path = nativeDeckPreviewPath(artifact.id, metadata.artifactVersion, metadata.contentDigest);
        }
        if (!path) throw new Error('Deck artifact binding is invalid.');
        if (!active) return;
        setDeckUrl(buildApiUrl(API_BASE_URL, path));
        setDeckHtml(null);
        setDeckLoading(false);
      } catch {
        if (!active) return;
        setDeckError(true);
        setDeckLoading(false);
      }
    })();
    return () => { active = false; };
  }, [kind, artifactDigest, artifactId, artifactVersion, sessionToken, loading, htmlContent, deckRetryNonce, resetDeckNavigation]);

  const deckSource = useMemo(() => deckUrl
    ? {
        uri: deckUrl,
        headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken, { Accept: 'text/html' }),
      }
    : { html: deckHtml ?? '' }, [deckHtml, deckUrl, sessionToken]);

  const navigateDeck = useCallback((direction: 'previous' | 'next') => {
    const target = deckPreviewNavigationTarget(deckNavigationRef.current, direction);
    const webView = deckWebViewRef.current;
    if (target === null || !webView) return;
    const next = { ...deckNavigationRef.current, currentIndex: target };
    updateDeckNavigation(next);
    webView.injectJavaScript(deckPreviewNavigationCommand(target));
  }, [updateDeckNavigation]);

  const retryDeck = useCallback(() => {
    setDeckError(false);
    resetDeckNavigation();
    setDeckRetryNonce((current) => current + 1);
  }, [resetDeckNavigation]);

  // html_deck: the 16:9 IS the slide
  if (isPresentable) {
    // Loading state during creation
    if (loading) {
      return (
        <View style={styles.deckContainer}>
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
        <View style={styles.deckContainer}>
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
    if (deckError || (!deckHtml && !deckUrl)) {
      return (
        <View style={styles.deckContainer}>
          <Glass radius={radius.lg} style={styles.deckGlass}>
            <View style={styles.deckLoadingCenter}>
              <SymbolView name="exclamationmark.triangle" size={32} tintColor={colors.text3} />
              <Text style={styles.deckErrorText}>Could not load deck</Text>
              <View style={styles.deckErrorActions}>
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Retry deck preview"
                  hitSlop={4}
                  onPress={retryDeck}
                  style={({ pressed }) => [styles.deckRetryButton, pressed && styles.deckRetryPressed]}
                >
                  <Text style={styles.deckRetryText}>Retry</Text>
                </Pressable>
                {onPresent ? (
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel="Open presentation"
                    hitSlop={4}
                    onPress={onPresent}
                    style={({ pressed }) => [styles.deckRetryButton, pressed && styles.deckRetryPressed]}
                  >
                    <Text style={styles.deckRetryText}>Open</Text>
                  </Pressable>
                ) : null}
              </View>
            </View>
          </Glass>
        </View>
      );
    }

    // Real deck: WebView fills the glass, artifact HTML is THE document
    return (
      <View style={styles.deckContainer}>
        <View style={styles.deckWebViewWrapper}>
          <WebView
            ref={deckWebViewRef}
            source={deckSource}
            style={[
              styles.deckWebViewFill,
              deckNavigation.status !== 'ready' && styles.deckWebViewHidden,
            ]}
            scrollEnabled={false}
            originWhitelist={['*']}
            javaScriptEnabled
            domStorageEnabled
            showsHorizontalScrollIndicator={false}
            showsVerticalScrollIndicator={false}
            injectedJavaScript={DECK_PREVIEW_NAVIGATION_JS}
            onLoadStart={resetDeckNavigation}
            onMessage={(event) => {
              const next = parseDeckPreviewNavigationMessage(event.nativeEvent.data);
              if (!next) return;
              updateDeckNavigation(next);
              if (next.status === 'error') setDeckError(true);
            }}
            onError={() => {
              updateDeckNavigation({ status: 'error', currentIndex: 0, slideCount: 0 });
              setDeckError(true);
            }}
            onHttpError={(event) => {
              if (event.nativeEvent.statusCode < 400) return;
              updateDeckNavigation({ status: 'error', currentIndex: 0, slideCount: 0 });
              setDeckError(true);
            }}
            onContentProcessDidTerminate={() => {
              updateDeckNavigation({ status: 'error', currentIndex: 0, slideCount: 0 });
              setDeckError(true);
            }}
          />
          {deckNavigation.status !== 'ready' ? (
            <View
              accessibilityLabel="Fitting presentation preview"
              accessibilityRole="progressbar"
              style={styles.deckFitLoading}
            >
              <ActivityIndicator color={colors.emberText} size="small" />
            </View>
          ) : null}
        </View>
        <View style={styles.deckNavigation} accessibilityLabel="Presentation slide navigation">
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Previous slide"
            accessibilityHint={deckNavigation.status === 'ready' ? `Currently slide ${deckNavigation.currentIndex + 1} of ${deckNavigation.slideCount}` : 'Deck preview is loading'}
            accessibilityState={{ disabled: deckNavigation.status !== 'ready' || deckNavigation.currentIndex === 0 }}
            disabled={deckNavigation.status !== 'ready' || deckNavigation.currentIndex === 0}
            hitSlop={4}
            onPress={() => navigateDeck('previous')}
            style={({ pressed }) => [
              styles.deckNavigationButton,
              (deckNavigation.status !== 'ready' || deckNavigation.currentIndex === 0) && styles.deckNavigationDisabled,
              pressed && styles.deckNavigationPressed,
            ]}
          >
            <SymbolView name="chevron.left" size={15} tintColor={colors.onAccent} />
          </Pressable>
          <Text
            accessibilityLiveRegion="polite"
            accessibilityLabel={deckNavigation.status === 'ready'
              ? `Slide ${deckNavigation.currentIndex + 1} of ${deckNavigation.slideCount}`
              : 'Deck preview loading'}
            style={styles.deckNavigationCount}
          >
            {deckNavigation.status === 'ready'
              ? `${deckNavigation.currentIndex + 1} / ${deckNavigation.slideCount}`
              : '— / —'}
          </Text>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Next slide"
            accessibilityHint={deckNavigation.status === 'ready' ? `Currently slide ${deckNavigation.currentIndex + 1} of ${deckNavigation.slideCount}` : 'Deck preview is loading'}
            accessibilityState={{ disabled: deckNavigation.status !== 'ready' || deckNavigation.currentIndex >= deckNavigation.slideCount - 1 }}
            disabled={deckNavigation.status !== 'ready' || deckNavigation.currentIndex >= deckNavigation.slideCount - 1}
            hitSlop={4}
            onPress={() => navigateDeck('next')}
            style={({ pressed }) => [
              styles.deckNavigationButton,
              (deckNavigation.status !== 'ready' || deckNavigation.currentIndex >= deckNavigation.slideCount - 1) && styles.deckNavigationDisabled,
              pressed && styles.deckNavigationPressed,
            ]}
          >
            <SymbolView name="chevron.right" size={15} tintColor={colors.onAccent} />
          </Pressable>
        </View>
        {needsAttention ? (
          <View accessibilityRole="summary" style={styles.deckDraftBanner}>
            <SymbolView name="exclamationmark.triangle.fill" size={12} tintColor={colors.emberText} />
            <Text style={styles.deckDraftText}>Draft · needs attention</Text>
          </View>
        ) : null}
        {desktopEditingOnly ? (
          <View accessibilityLabel="Editing is available on desktop" style={styles.deckDesktopBadge}>
            <SymbolView name="desktopcomputer" size={12} tintColor={colors.onAccent} />
            <Text maxFontSizeMultiplier={1.4} style={styles.deckDesktopText}>Edit on desktop</Text>
          </View>
        ) : null}
        {/* Mobile is deliberately read-only; Present is the only deck action. */}
        <View style={styles.deckOverlayActions}>
          {onPresent ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Present"
              hitSlop={4}
              onPress={onPresent}
              style={({ pressed }) => [styles.deckActionButton, styles.deckActionPrimary, pressed && styles.deckActionPressed]}
            >
              <SymbolView name="play.fill" size={14} tintColor={colors.onAccent} />
              <Text style={styles.deckActionText}>Present</Text>
            </Pressable>
          ) : previewOnlyLabel ? (
            <View
              accessibilityLabel={`${previewOnlyLabel}. Full-screen presentation is unavailable for this archived deck.`}
              accessibilityRole="summary"
              style={styles.deckPreviewOnlyBadge}
            >
              <SymbolView name="eye" size={14} tintColor={colors.onAccent} />
              <Text maxFontSizeMultiplier={1.4} style={styles.deckPreviewOnlyText}>{previewOnlyLabel}</Text>
            </View>
          ) : null}
        </View>
      </View>
    );
  }

	const validAssets = assets.slice(0, 12).filter((asset) => (
		/^[0-9a-f]{64}$/u.test(String(asset?.ref ?? '').trim().toLowerCase())
		&& String(asset?.kind ?? '').trim().toLowerCase() !== 'page_image'
	));
	const structured = ['pdf', 'image', 'table', 'workbook', 'bundle', 'file'].includes(kind);
	if (structured) {
		const imageAsset = kind === 'image'
			? validAssets.find((asset) => String(asset.kind ?? '').toLowerCase() === 'image' && String(asset.mime ?? '').toLowerCase().startsWith('image/'))
			: undefined;
		const primaryAsset = kind === 'pdf'
			? validAssets.find((asset) => ['pdf', 'export'].includes(String(asset.kind ?? '').toLowerCase()) && String(asset.mime ?? '').toLowerCase() === 'application/pdf')
			: kind === 'workbook'
				? validAssets.find((asset) => String(asset.kind ?? '').toLowerCase() === 'export')
				: imageAsset ?? validAssets[0];
		const tableColumns = Array.isArray(table?.columns) ? table.columns.slice(0, 12) : [];
		const tableRows = Array.isArray(table?.rows) ? table.rows.slice(0, 20) : [];
		return (
			<Glass radius={radius.lg} style={styles.structuredContainer}>
				<View style={styles.header}>
					<View style={styles.headerLeft}>
						<View style={styles.kindBadge}>
							<SymbolView name={kindIcon[kind] as any} size={12} tintColor={colors.emberText} />
							<Text style={styles.kindText}>{kindLabel[kind]}</Text>
						</View>
						<Text numberOfLines={1} style={styles.byline}>{agentName} · {needsAttention ? 'draft · needs attention' : 'delivered'}</Text>
					</View>
				</View>
				<Text accessibilityRole="header" numberOfLines={2} style={styles.title}>{title}</Text>
				<View style={styles.structuredPreview}>
					{kind === 'image' && imageAsset && sessionToken ? (
						<Image
							accessibilityLabel={`Preview of ${title}`}
							cachePolicy="memory-disk"
							contentFit="contain"
							recyclingKey={`${imageAsset.ref}-inline-result`}
							source={{ uri: authenticatedFileUrl({ ref: imageAsset.ref, name: imageAsset.name || 'image', mime: imageAsset.mime }), headers: authenticatedFileHeaders(sessionToken, imageAsset.mime) }}
							style={styles.structuredImage}
						/>
					) : kind === 'table' && tableColumns.length ? (
						<ScrollView horizontal nestedScrollEnabled showsHorizontalScrollIndicator contentContainerStyle={styles.tableCanvas}>
							<View accessibilityLabel={`${title} data table`} accessibilityRole="summary" style={styles.tableGrid}>
								<View style={styles.tableRow}>
									{tableColumns.map((column, index) => <Text key={`${index}-${column}`} numberOfLines={2} style={[styles.tableCell, styles.tableHeader]}>{column}</Text>)}
								</View>
								{tableRows.map((row, rowIndex) => (
									<View key={`row-${rowIndex}`} style={styles.tableRow}>
										{tableColumns.map((_, cellIndex) => <Text key={`cell-${rowIndex}-${cellIndex}`} numberOfLines={3} style={styles.tableCell}>{String(row?.[cellIndex] ?? '')}</Text>)}
									</View>
								))}
							</View>
						</ScrollView>
					) : kind === 'workbook' && workbook ? (
						<View style={styles.workbookPreview}>
							<View style={styles.workbookFacts}>
								<View style={styles.workbookFact}><Text style={styles.workbookFactValue}>{workbook.sheetCount}</Text><Text style={styles.workbookFactLabel}>Sheets</Text></View>
								<View style={styles.workbookFact}><Text style={styles.workbookFactValue}>{workbook.formulaCount}</Text><Text style={styles.workbookFactLabel}>Formulas</Text></View>
							</View>
							<ScrollView nestedScrollEnabled showsVerticalScrollIndicator={false} style={styles.sheetList}>
								{(workbook.sheets ?? []).slice(0, 12).map((sheet, index) => (
									<View key={`${index}-${sheet.name}`} style={styles.sheetRow}><Text numberOfLines={1} style={styles.sheetName}>{sheet.name}</Text><Text numberOfLines={2} style={styles.sheetPurpose}>{sheet.purpose || 'Workbook sheet'}</Text></View>
								))}
							</ScrollView>
						</View>
					) : kind === 'bundle' || kind === 'file' ? (
						<ScrollView nestedScrollEnabled showsVerticalScrollIndicator={false}>
							{validAssets.map((asset, index) => (
								<Pressable key={`${asset.ref}-${index}`} accessibilityRole="button" accessibilityLabel={`Open ${asset.name || 'file'}`} onPress={() => onOpenAsset?.(asset)} style={({ pressed }) => [styles.fileRow, pressed && styles.actionPressed]}>
									<SymbolView name={String(asset.mime ?? '').startsWith('image/') ? 'photo' : 'doc'} size={18} tintColor={colors.emberText} />
									<View style={styles.fileCopy}><Text numberOfLines={1} style={styles.fileName}>{asset.name || 'File'}</Text><Text numberOfLines={1} style={styles.fileMeta}>{asset.mime || 'File'}</Text></View>
									<SymbolView name="arrow.up.right" size={14} tintColor={colors.text3} />
								</Pressable>
							))}
						</ScrollView>
					) : kind === 'pdf' && primaryAsset ? (
						<View accessibilityLabel={`${title} PDF ready`} accessibilityRole="image" style={styles.pdfPreview}>
							<SymbolView name="doc.richtext" size={44} tintColor={colors.emberText} />
							<Text style={styles.pdfLabel}>Authenticated PDF</Text>
						</View>
					) : (
						<View accessibilityRole="alert" style={styles.structuredUnavailable}><SymbolView name="exclamationmark.triangle" size={24} tintColor={colors.text3} /><Text style={styles.structuredUnavailableText}>Preview unavailable</Text></View>
					)}
				</View>
				{primaryAsset && kind !== 'bundle' && kind !== 'file' ? (
					<Pressable accessibilityRole="button" accessibilityLabel={kind === 'workbook' ? 'Open workbook file' : `Open ${kindLabel[kind]}`} onPress={() => onOpenAsset?.(primaryAsset)} style={({ pressed }) => [styles.structuredAction, pressed && styles.actionPressed]}>
						<SymbolView name={kind === 'workbook' ? 'arrow.down.doc' : 'arrow.up.left.and.arrow.down.right'} size={13} tintColor={colors.text2} />
						<Text style={styles.fullscreenText}>{kind === 'workbook' ? 'Open XLSX' : 'Full screen'}</Text>
					</Pressable>
				) : null}
			</Glass>
		);
	}

  // Non-deck previews are prose, never serialized Studio state or markup. The
  // authoritative full-screen route can still open the real artifact, but an
  // unexpected JSON payload must fail closed instead of reproducing the raw
  // code screen that older iOS builds exposed.
  const previewText = nativeTextArtifactIsRenderable(text)
    ? text
    : `${kindLabel[kind]} is ready to open.`;
  const previewLines = previewText.split('\n').slice(0, expanded ? undefined : 12);
  const hasMore = previewText.split('\n').length > 12;

  return (
    <Glass radius={radius.lg} style={styles.container}>
      <View style={styles.header}>
        <View style={styles.headerLeft}>
          <View style={styles.kindBadge}>
            <SymbolView name={kindIcon[kind] as any} size={12} tintColor={colors.emberText} />
            <Text style={styles.kindText}>{kindLabel[kind]}</Text>
          </View>
          <Text style={styles.byline}>{agentName} · {needsAttention ? 'draft · needs attention' : 'delivered'}</Text>
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

      {kind === 'document' && onEdit ? (
        <View style={styles.documentActions}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Edit document"
            onPress={onEdit}
            style={({ pressed }) => [styles.documentEditButton, pressed && styles.actionPressed]}
          >
            <SymbolView name="pencil" size={12} tintColor={colors.onAccent} />
            <Text style={styles.documentEditText}>Edit</Text>
          </Pressable>
          {onExpand ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Open in full screen"
              onPress={onExpand}
              style={({ pressed }) => [styles.documentFullscreenButton, pressed && styles.actionPressed]}
            >
              <SymbolView name="arrow.up.left.and.arrow.down.right" size={12} tintColor={colors.text2} />
              <Text style={styles.fullscreenText}>Full screen</Text>
            </Pressable>
          ) : null}
        </View>
      ) : onExpand ? (
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
	structuredContainer: {
		width: '100%',
		maxWidth: 680,
		minHeight: 240,
		padding: space[4],
		gap: space[3],
	},
	structuredPreview: {
		minHeight: 150,
		maxHeight: 340,
		overflow: 'hidden',
		borderRadius: radius.md,
		borderCurve: 'continuous',
		backgroundColor: colors.surface2,
	},
	structuredImage: {
		width: '100%',
		height: 300,
		backgroundColor: colors.surface1,
		borderWidth: StyleSheet.hairlineWidth,
		borderColor: 'rgba(0,0,0,0.18)',
	},
	tableCanvas: { minWidth: '100%' },
	tableGrid: { minWidth: 440, backgroundColor: colors.surface1 },
	tableRow: { flexDirection: 'row', borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.border },
	tableCell: {
		width: 140,
		minHeight: 44,
		paddingHorizontal: space[3],
		paddingVertical: space[2],
		...type.caption,
		color: colors.text1,
		fontVariant: ['tabular-nums'],
	},
	tableHeader: { ...type.label, color: colors.text2, backgroundColor: colors.surface2 },
	workbookPreview: { minHeight: 180, flexDirection: 'row', gap: space[3], padding: space[3] },
	workbookFacts: { width: 116, gap: space[2] },
	workbookFact: { flex: 1, minHeight: 72, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.surface1 },
	workbookFactValue: { ...type.title2, color: colors.text1, fontVariant: ['tabular-nums'] },
	workbookFactLabel: { ...type.label, color: colors.text3, textTransform: 'uppercase' },
	sheetList: { flex: 1 },
	sheetRow: { padding: space[3], marginBottom: space[2], borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.surface1 },
	sheetName: { ...type.captionMedium, color: colors.text1 },
	sheetPurpose: { ...type.caption, color: colors.text3, marginTop: 2 },
	fileRow: { minHeight: 58, flexDirection: 'row', alignItems: 'center', gap: space[3], marginBottom: space[2], paddingHorizontal: space[3], borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.surface1 },
	fileCopy: { minWidth: 0, flex: 1 },
	fileName: { ...type.captionMedium, color: colors.text1 },
	fileMeta: { ...type.label, color: colors.text3, marginTop: 2 },
	pdfPreview: { minHeight: 190, alignItems: 'center', justifyContent: 'center', gap: space[3] },
	pdfLabel: { ...type.captionMedium, color: colors.text2 },
	structuredUnavailable: { minHeight: 190, alignItems: 'center', justifyContent: 'center', gap: space[2] },
	structuredUnavailableText: { ...type.caption, color: colors.text3 },
	structuredAction: { minHeight: 44, alignSelf: 'flex-end', flexDirection: 'row', alignItems: 'center', gap: 6, paddingHorizontal: space[3], borderRadius: radius.full, borderCurve: 'continuous', backgroundColor: colors.surface2 },
  // Deck-specific styles (16:9 IS the slide)
  deckContainer: {
    width: '100%',
    maxWidth: 680,
    aspectRatio: 16 / 9,
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
  deckWebViewHidden: { opacity: 0 },
  deckFitLoading: {
    position: 'absolute',
    inset: 0,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.bgApp,
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
    minHeight: 44,
    justifyContent: 'center',
    paddingHorizontal: space[4],
    paddingVertical: space[2],
    borderRadius: radius.full,
    borderCurve: 'continuous',
    backgroundColor: colors.surface3,
  },
  deckErrorActions: {
    marginTop: space[2],
    flexDirection: 'row',
    gap: space[2],
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
  deckDesktopBadge: {
    position: 'absolute',
    left: space[3],
    bottom: space[3],
    minHeight: 36,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    borderCurve: 'continuous',
    backgroundColor: 'rgba(0,0,0,0.66)',
  },
  deckDesktopText: { ...type.label, color: colors.onAccent },
  deckDraftBanner: {
    position: 'absolute',
    left: space[3],
    top: space[3],
    minHeight: 36,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    backgroundColor: colors.surface1,
  },
  deckDraftText: { ...type.captionMedium, color: colors.emberText },
  deckActionButton: {
    minHeight: 44,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    borderCurve: 'continuous',
    backgroundColor: 'rgba(0,0,0,0.6)',
  },
  deckActionPrimary: {
    backgroundColor: colors.accent,
  },
  deckActionPressed: {
    opacity: 0.8,
    transform: [{ scale: 0.96 }],
  },
  deckActionText: {
    ...type.captionMedium,
    color: colors.onAccent,
  },
  deckPreviewOnlyBadge: {
    minHeight: 36,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    borderCurve: 'continuous',
    backgroundColor: 'rgba(0,0,0,0.66)',
  },
  deckPreviewOnlyText: {
    ...type.label,
    color: colors.onAccent,
  },
  deckNavigation: {
    position: 'absolute',
    top: space[3],
    right: space[3],
    minHeight: 44,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[1],
    paddingHorizontal: 4,
    borderRadius: radius.full,
    borderCurve: 'continuous',
    backgroundColor: 'rgba(0,0,0,0.72)',
  },
  deckNavigationButton: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    borderCurve: 'continuous',
  },
  deckNavigationPressed: {
    backgroundColor: 'rgba(255,255,255,0.16)',
  },
  deckNavigationDisabled: {
    opacity: 0.36,
  },
  deckNavigationCount: {
    minWidth: 48,
    textAlign: 'center',
    ...type.captionMedium,
    color: colors.onAccent,
    fontVariant: ['tabular-nums'],
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
  documentActions: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'flex-end',
    gap: space[2],
  },
  documentEditButton: {
    minHeight: 44,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    borderCurve: 'continuous',
    backgroundColor: colors.accent,
  },
  documentEditText: {
    ...type.captionMedium,
    color: colors.onAccent,
  },
  documentFullscreenButton: {
    minHeight: 44,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 6,
    paddingHorizontal: space[3],
    borderRadius: radius.full,
    borderCurve: 'continuous',
    backgroundColor: colors.surface2,
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
    transform: [{ scale: 0.96 }],
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
    minHeight: 44,
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
    minHeight: 44,
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
