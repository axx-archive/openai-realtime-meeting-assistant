import React, { useEffect, useMemo } from 'react';
import { ActivityIndicator, Alert, Pressable, StyleSheet, Text, View } from 'react-native';
import { Image } from 'expo-image';
import { SymbolView } from 'expo-symbols';
import * as Linking from 'expo-linking';
import { useRecyclingState } from '@shopify/flash-list';

import { api } from '../api/client';
import type { LinkPreview } from '../api/types';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';
import { buildApiUrl, buildAuthHeaders } from '../api/requestHelpers';
import { colors, radius, space, type } from '../theme/tokens';
import { cachedLinkPreview } from './linkPreviewCache';
import { messageLongPressDelayMs } from './messageGestures';

function hostLabel(raw: string): string {
  try {
    return new URL(raw).hostname.replace(/^www\./i, '');
  } catch {
    return 'Link';
  }
}

type Props = {
  url: string;
  sessionToken: string;
  own: boolean;
  seamless?: boolean;
  onLongPress?: () => void;
};

export const LinkPreviewCard = React.memo(function LinkPreviewCard({ url, sessionToken, own, seamless = false, onLongPress }: Props) {
  // The bounded disk cache owns freshness. A second permanent cache here
  // would pin transient failures and defeat its expiry policy.
  const key = `${sessionToken}:${url}`;
  const [preview, setPreview] = useRecyclingState<LinkPreview | null | undefined>(undefined, [key]);
  const [imageFailed, setImageFailed] = useRecyclingState(false, [key, preview?.imageUrl]);
  useEffect(() => {
    let active = true;
    void cachedLinkPreview(url, () => api.linkPreview(sessionToken, url).then((result) => result.preview))
      .then((value) => { if (active) setPreview(value); })
      .catch(() => { if (active) setPreview(null); });
    return () => { active = false; };
  }, [key, sessionToken, setPreview, url]);

  const openLink = (destination: string) => {
    void Linking.openURL(destination).catch(() => {
      Alert.alert('Link could not open', 'Try this link again when its app or website is available.');
    });
  };

  const imageSource = useMemo(() => {
    const path = preview?.imageUrl?.trim();
    if (imageFailed || !path?.startsWith('/assistant/link-preview/image?')) return null;
    return {
      uri: buildApiUrl(API_BASE_URL, path),
      headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken, { Accept: 'image/*' }),
    };
  }, [imageFailed, preview?.imageUrl, sessionToken]);

  const site = preview?.siteName?.trim() || hostLabel(url);
  const title = preview?.title?.trim() || hostLabel(url);
  const description = preview?.description?.trim();
  const destination = /^https?:\/\//i.test(preview?.url?.trim() || '') ? preview!.url.trim() : url;

  if (preview === undefined) {
    if (!seamless) return null;
    return (
      <View style={[styles.card, styles.cardSeamless, own && styles.cardSeamlessOwn, styles.loadingCard]}>
        <SymbolView name="link" tintColor={own ? colors.onAccent : colors.text2} size={13} />
        <Text numberOfLines={1} style={[styles.loadingHost, own && styles.copyOwn]}>{hostLabel(url)}</Text>
        <ActivityIndicator size="small" color={own ? colors.onAccent : colors.text3} />
      </View>
    );
  }

  if (preview === null) {
    if (!seamless) return null;
    return (
      <Pressable
        accessibilityRole="link"
        accessibilityLabel={`Open ${url}`}
        onPress={() => openLink(url)}
        onLongPress={onLongPress}
        delayLongPress={messageLongPressDelayMs}
        style={({ pressed }) => [styles.card, styles.cardSeamless, styles.plainCard, own && styles.cardSeamlessOwn, pressed && styles.pressed]}
      >
        <View style={[styles.plainIcon, own && styles.plainIconOwn]}><SymbolView name="link" tintColor={own ? colors.onAccent : colors.text2} size={15} /></View>
        <View style={styles.authorCopy}>
          <Text style={[styles.title, own && styles.copyOwn]}>{hostLabel(url)}</Text>
          <Text numberOfLines={2} style={[styles.site, own && styles.descriptionOwn]}>{url}</Text>
        </View>
      </Pressable>
    );
  }

  if (preview.kind === 'x_post') {
    const author = preview.authorName?.trim() || title;
    const handle = preview.authorHandle?.trim();
    const avatar = preview.imageRole === 'author_avatar';
    return (
      <Pressable
        accessibilityRole="link"
        accessibilityLabel={`${author} on X: ${description || 'Post'}`}
        accessibilityHint="Opens this post on X"
        onPress={() => openLink(url)}
        onLongPress={onLongPress}
        delayLongPress={messageLongPressDelayMs}
        style={({ pressed }) => [
          styles.card,
          styles.postCard,
          seamless && styles.cardSeamless,
          own && (seamless ? styles.cardSeamlessOwn : styles.cardOwn),
          pressed && styles.pressed,
        ]}
      >
        <View style={styles.postHeader}>
          {imageSource && avatar ? (
            <Image onError={() => setImageFailed(true)} source={imageSource} cachePolicy="memory-disk" contentFit="cover" enforceEarlyResizing recyclingKey={`${url}-avatar`} style={styles.avatar} />
          ) : (
            <View style={[styles.avatar, styles.avatarFallback]}><Text style={styles.avatarInitial}>{author.slice(0, 1)}</Text></View>
          )}
          <View style={styles.authorCopy}>
            <Text numberOfLines={1} style={[styles.author, own && styles.copyOwn]}>{author}</Text>
            {handle ? <Text numberOfLines={1} style={[styles.handle, own && styles.descriptionOwn]}>@{handle}</Text> : null}
          </View>
          <Text style={[styles.xMark, own && styles.copyOwn]}>𝕏</Text>
        </View>
        {description ? <Text numberOfLines={5} style={[styles.postText, own && styles.copyOwn]}>{description}</Text> : null}
        {imageSource && !avatar ? <View style={[styles.hero, styles.postMedia]}>
          <Image source={imageSource} accessibilityLabel="Post image" cachePolicy="memory-disk" contentFit="cover"
            enforceEarlyResizing onError={() => setImageFailed(true)} recyclingKey={`${url}-post-media`} style={StyleSheet.absoluteFill} />
        </View> : null}
        <View style={styles.postFooter}>
          <Text numberOfLines={1} style={[styles.postDate, own && styles.descriptionOwn]}>{preview.publishedAt ? `${preview.publishedAt} · x.com` : 'x.com'}</Text>
          <SymbolView name="arrow.up.right" tintColor={own ? colors.onAccent : colors.text3} size={10} />
        </View>
      </Pressable>
    );
  }

  if (preview.kind === 'tiktok_video') {
    const author = preview.authorName?.trim();
    const handle = preview.authorHandle?.trim();
    const creator = author && handle ? `${author} · @${handle}` : author || (handle ? `@${handle}` : 'TikTok creator');
    return (
      <Pressable
        accessibilityRole="link"
        accessibilityLabel={`Play ${title} by ${creator} on TikTok`}
        accessibilityHint="Opens the original video in TikTok"
        onPress={() => openLink(destination)}
        onLongPress={onLongPress}
        delayLongPress={messageLongPressDelayMs}
        style={({ pressed }) => [
          styles.card,
          styles.visualCard,
          styles.tikTokCard,
          seamless && styles.cardSeamless,
          pressed && styles.pressed,
        ]}
      >
        <View style={[styles.hero, styles.tikTokHero]}>
          {imageSource ? (
            <Image
            onError={() => setImageFailed(true)}
              source={imageSource}
              cachePolicy="memory-disk"
              contentFit="cover"
              enforceEarlyResizing
              recyclingKey={`${url}-tiktok-poster`}
              style={StyleSheet.absoluteFill}
            />
          ) : (
            <View style={[StyleSheet.absoluteFill, styles.tikTokPosterFallback]} />
          )}
          <View pointerEvents="none" style={[styles.imageOutline, styles.tikTokImageOutline]} />
          <View pointerEvents="none" style={styles.tikTokBrand}>
            <Text style={styles.tikTokBrandText}>TikTok</Text>
          </View>
          <View pointerEvents="none" style={styles.playButton}>
            <SymbolView name="play.fill" tintColor="#FFFFFF" size={27} style={styles.playGlyph} />
          </View>
        </View>
        <View style={[styles.visualCopy, styles.tikTokCopy]}>
          <Text numberOfLines={3} style={styles.visualTitle}>{title}</Text>
          <Text numberOfLines={1} style={styles.tikTokCreator}>{creator}</Text>
          <View style={styles.tikTokFooter}>
            <Text style={styles.visualSite}>TikTok · video</Text>
            <SymbolView name="arrow.up.right" tintColor={colors.text3} size={10} />
          </View>
        </View>
      </Pressable>
    );
  }

  if (preview.kind === 'youtube_video') {
    if (!imageSource) {
      return (
        <Pressable
          accessibilityRole="link"
          accessibilityLabel={`Open ${title} on YouTube`}
          accessibilityHint="Opens the original video on YouTube"
          onPress={() => openLink(destination)}
          onLongPress={onLongPress}
          delayLongPress={messageLongPressDelayMs}
          style={({ pressed }) => [styles.card, styles.providerFallbackCard, seamless && styles.cardSeamless, pressed && styles.pressed]}
        >
          <View style={styles.providerFallbackHeader}>
            <View style={styles.youTubeFallbackMark}><SymbolView name="play.fill" tintColor="#FFFFFF" size={13} /></View>
            <Text style={styles.providerFallbackName}>YouTube · video</Text>
          </View>
          <Text numberOfLines={2} style={styles.title}>{title}</Text>
          {description ? <Text numberOfLines={2} style={styles.description}>{description}</Text> : null}
        </Pressable>
      );
    }
    return (
      <Pressable
        accessibilityRole="link"
        accessibilityLabel={`Play ${title} on YouTube`}
        accessibilityHint="Opens the original video on YouTube"
        onPress={() => openLink(destination)}
        onLongPress={onLongPress}
        delayLongPress={messageLongPressDelayMs}
        style={({ pressed }) => [styles.card, styles.visualCard, styles.youTubeCard, seamless && styles.cardSeamless, pressed && styles.pressed]}
      >
        <View style={styles.hero}>
          <Image
            onError={() => setImageFailed(true)}
            source={imageSource}
            cachePolicy="memory-disk"
            contentFit="cover"
            enforceEarlyResizing
            recyclingKey={`${url}-youtube-poster`}
            style={StyleSheet.absoluteFill}
          />
          <View pointerEvents="none" style={styles.imageOutline} />
          <View pointerEvents="none" style={styles.youTubeBrand}><Text style={styles.providerBrandText}>YouTube</Text></View>
          <View pointerEvents="none" style={styles.playButton}>
            <SymbolView name="play.fill" tintColor="#FFFFFF" size={27} style={styles.playGlyph} />
          </View>
        </View>
        <View style={styles.visualCopy}>
          <Text numberOfLines={2} style={styles.visualTitle}>{title}</Text>
          <Text numberOfLines={1} style={styles.visualSite}>{description || 'YouTube · video'}</Text>
        </View>
      </Pressable>
    );
  }

  if (preview.kind === 'instagram_reel' || preview.kind === 'instagram_video' || preview.kind === 'instagram_post') {
    const instagramVideo = preview.kind !== 'instagram_post';
    const format = preview.kind === 'instagram_reel' ? 'reel' : instagramVideo ? 'video' : 'post';
    if (!imageSource) {
      return (
        <Pressable
          accessibilityRole="link"
          accessibilityLabel={`Open ${title} on Instagram`}
          accessibilityHint={`Opens the original Instagram ${format}`}
          onPress={() => openLink(destination)}
          onLongPress={onLongPress}
          delayLongPress={messageLongPressDelayMs}
          style={({ pressed }) => [styles.card, styles.providerFallbackCard, styles.instagramFallbackCard, seamless && styles.cardSeamless, pressed && styles.pressed]}
        >
          <View style={styles.providerFallbackHeader}>
            <View style={styles.instagramFallbackMark}><Text style={styles.instagramFallbackGlyph}>◎</Text></View>
            <Text style={styles.providerFallbackName}>Instagram · {format}</Text>
          </View>
          <Text numberOfLines={2} style={styles.title}>{title}</Text>
          {description ? <Text numberOfLines={2} style={styles.description}>{description}</Text> : null}
        </Pressable>
      );
    }
    return (
      <Pressable
        accessibilityRole="link"
        accessibilityLabel={`${instagramVideo ? 'Play' : 'Open'} ${title} on Instagram`}
        accessibilityHint={`Opens the original Instagram ${format}`}
        onPress={() => openLink(destination)}
        onLongPress={onLongPress}
        delayLongPress={messageLongPressDelayMs}
        style={({ pressed }) => [styles.card, styles.visualCard, styles.instagramCard, seamless && styles.cardSeamless, pressed && styles.pressed]}
      >
        <View style={[styles.hero, instagramVideo ? styles.instagramVideoHero : styles.instagramPostHero]}>
          <Image
            onError={() => setImageFailed(true)}
            source={imageSource}
            cachePolicy="memory-disk"
            contentFit="cover"
            enforceEarlyResizing
            recyclingKey={`${url}-instagram-${format}`}
            style={StyleSheet.absoluteFill}
          />
          <View pointerEvents="none" style={[styles.imageOutline, styles.instagramImageOutline]} />
          <View pointerEvents="none" style={styles.instagramBrand}><Text style={styles.providerBrandText}>Instagram</Text></View>
          {instagramVideo ? (
            <View pointerEvents="none" style={styles.playButton}>
              <SymbolView name="play.fill" tintColor="#FFFFFF" size={27} style={styles.playGlyph} />
            </View>
          ) : null}
        </View>
        <View style={styles.visualCopy}>
          <Text numberOfLines={2} style={styles.visualTitle}>{title}</Text>
          <Text numberOfLines={2} style={styles.visualSite}>{description || `Instagram · ${format}`}</Text>
        </View>
      </Pressable>
    );
  }

  const playable = preview.kind === 'video';
  const visual = Boolean(imageSource);

  if (visual) {
    return (
      <Pressable
        accessibilityRole="link"
        accessibilityLabel={`${playable ? 'Play' : 'Open'} ${title}, ${site}`}
        accessibilityHint={`Opens this ${playable ? 'video' : 'website'}`}
        onPress={() => openLink(url)}
        onLongPress={onLongPress}
        delayLongPress={messageLongPressDelayMs}
        style={({ pressed }) => [
          styles.card,
          styles.visualCard,
          seamless && styles.cardSeamless,
          pressed && styles.pressed,
        ]}
      >
        <View style={styles.hero}>
          <Image
            onError={() => setImageFailed(true)}
            source={imageSource}
            cachePolicy="memory-disk"
            contentFit="cover"
            enforceEarlyResizing
            recyclingKey={url}
            style={StyleSheet.absoluteFill}
          />
          <View pointerEvents="none" style={styles.imageOutline} />
          {playable ? (
            <View pointerEvents="none" style={styles.playButton}>
              <SymbolView name="play.fill" tintColor="#FFFFFF" size={27} style={styles.playGlyph} />
            </View>
          ) : null}
        </View>
        <View style={styles.visualCopy}>
          <Text numberOfLines={2} style={styles.visualTitle}>{title}</Text>
          {description && description !== title ? <Text numberOfLines={2} style={styles.visualDescription}>{description}</Text> : null}
          <View style={styles.visualFooter}>
            <Text numberOfLines={1} style={styles.visualSite}>{preview.kind === 'article' ? `${site} · article` : site}</Text>
            <SymbolView name="arrow.up.right" tintColor={colors.text3} size={10} />
          </View>
        </View>
      </Pressable>
    );
  }

  return (
    <Pressable
      accessibilityRole="link"
      accessibilityLabel={`${title}, ${site}`}
      accessibilityHint="Opens this link"
      onPress={() => openLink(url)}
      onLongPress={onLongPress}
      delayLongPress={messageLongPressDelayMs}
      style={({ pressed }) => [
        styles.card,
        seamless && styles.cardSeamless,
        own && (seamless ? styles.cardSeamlessOwn : styles.cardOwn),
        pressed && styles.pressed,
      ]}
    >
      <View style={styles.copy}>
        <View style={styles.siteRow}>
          <SymbolView name="link" tintColor={own ? colors.onAccent : colors.text2} size={11} />
          <Text numberOfLines={1} style={[styles.site, own && styles.copyOwn]}>{site}</Text>
        </View>
        <Text numberOfLines={2} style={[styles.title, own && styles.copyOwn]}>{title}</Text>
        {description && description !== title ? (
          <Text numberOfLines={2} style={[styles.description, own && styles.descriptionOwn]}>{description}</Text>
        ) : null}
      </View>
    </Pressable>
  );
});

const styles = StyleSheet.create({
  card: {
    width: 300,
    minWidth: 0,
    maxWidth: '100%',
    overflow: 'hidden',
    marginTop: space[2],
    borderRadius: radius.md,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface2,
  },
  cardSeamless: { marginTop: 0, borderWidth: 0, borderRadius: radius.lg, backgroundColor: colors.surface1 },
  cardSeamlessOwn: { backgroundColor: colors.accent },
  loadingCard: { minHeight: 76, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[3] },
  loadingHost: { ...type.bodyMedium, flex: 1, color: colors.text1 },
  plainCard: { minHeight: 84, flexDirection: 'row', alignItems: 'center', gap: space[3], padding: space[3] },
  plainIcon: { width: 38, height: 38, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md, backgroundColor: colors.surface3 },
  plainIconOwn: { backgroundColor: 'rgba(255,255,255,0.14)' },
  postCard: { gap: space[3], padding: space[3] },
  postHeader: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  avatar: { width: 38, height: 38, borderRadius: 19, backgroundColor: colors.surface3 },
  avatarFallback: { alignItems: 'center', justifyContent: 'center' },
  avatarInitial: { ...type.bodyMedium, color: colors.text2 },
  authorCopy: { flex: 1, minWidth: 0 },
  author: { ...type.bodyMedium, color: colors.text1 },
  handle: { ...type.caption, color: colors.text2 },
  xMark: { fontSize: 19, lineHeight: 23, color: colors.text1 },
  postMedia: { borderRadius: radius.md },
  postText: { ...type.body, color: colors.text1 },
  postDate: { ...type.caption, flex: 1, color: colors.text3 },
  postFooter: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[2] },
  cardOwn: { backgroundColor: 'rgba(0,0,0,0.08)', borderColor: 'rgba(0,0,0,0.10)' },
  visualCard: { gap: 0, backgroundColor: colors.surface2 },
  hero: { width: '100%', aspectRatio: 16 / 9, overflow: 'hidden', backgroundColor: colors.surface3 },
  imageOutline: { ...StyleSheet.absoluteFill, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
	providerFallbackCard: { minHeight: 116, gap: space[2], justifyContent: 'center', padding: space[3] },
	providerFallbackHeader: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
	providerFallbackName: { ...type.caption, flexShrink: 1, color: colors.text2, fontWeight: '700' },
	youTubeFallbackMark: { width: 28, height: 22, alignItems: 'center', justifyContent: 'center', borderRadius: 7, backgroundColor: '#E32636' },
	youTubeCard: { width: 300 },
	youTubeBrand: {
	  position: 'absolute',
	  top: space[3],
	  left: space[3],
	  minHeight: 30,
	  justifyContent: 'center',
	  paddingHorizontal: 10,
	  borderRadius: 15,
	  borderWidth: StyleSheet.hairlineWidth,
	  borderColor: 'rgba(255,255,255,0.12)',
	  backgroundColor: 'rgba(9,9,11,0.68)',
	},
	instagramCard: { width: 272, minWidth: 0, maxWidth: '100%' },
	instagramVideoHero: { aspectRatio: 4 / 5, backgroundColor: '#171215' },
	instagramPostHero: { aspectRatio: 1, backgroundColor: '#171215' },
	instagramImageOutline: { borderColor: 'rgba(255,255,255,0.10)' },
	instagramBrand: {
	  position: 'absolute',
	  top: space[3],
	  left: space[3],
	  minHeight: 30,
	  justifyContent: 'center',
	  paddingHorizontal: 10,
	  borderRadius: 15,
	  borderWidth: StyleSheet.hairlineWidth,
	  borderColor: 'rgba(255,255,255,0.14)',
	  backgroundColor: 'rgba(22,14,18,0.70)',
	},
	providerBrandText: { ...type.caption, color: '#FFFFFF', fontWeight: '700', letterSpacing: 0.2 },
	instagramFallbackCard: { borderColor: 'rgba(216,95,115,0.26)' },
	instagramFallbackMark: { width: 28, height: 28, alignItems: 'center', justifyContent: 'center', borderRadius: 14, backgroundColor: 'rgba(216,95,115,0.14)' },
	instagramFallbackGlyph: { color: '#D85F73', fontSize: 20, lineHeight: 22, fontWeight: '700' },
	  tikTokCard: { width: 272, minWidth: 0, maxWidth: '100%' },
	  tikTokHero: { aspectRatio: 3 / 4, backgroundColor: '#09090B' },
	  tikTokPosterFallback: { backgroundColor: '#141418' },
	  tikTokImageOutline: { borderColor: 'rgba(255,255,255,0.10)' },
	  tikTokBrand: {
	    position: 'absolute',
	    top: space[3],
	    left: space[3],
	    minHeight: 30,
	    justifyContent: 'center',
	    paddingHorizontal: 10,
	    borderRadius: 15,
	    borderWidth: StyleSheet.hairlineWidth,
	    borderColor: 'rgba(255,255,255,0.12)',
	    backgroundColor: 'rgba(9,9,11,0.68)',
	  },
	  tikTokBrandText: { ...type.caption, color: '#FFFFFF', fontWeight: '700', letterSpacing: 0.2 },
  playButton: {
    position: 'absolute',
    left: '50%',
    top: '50%',
    width: 58,
    height: 58,
    marginLeft: -29,
    marginTop: -29,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 29,
    backgroundColor: 'rgba(8,8,10,0.64)',
  },
  playGlyph: { transform: [{ translateX: 2 }] },
  visualCopy: { gap: 2, paddingHorizontal: space[3], paddingTop: 10, paddingBottom: space[3], backgroundColor: colors.surface3 },
	  tikTokCopy: { gap: 5, paddingTop: space[3] },
	  tikTokCreator: { ...type.caption, color: colors.text2 },
	  tikTokFooter: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[2] },
  visualTitle: { ...type.bodyMedium, color: colors.text1 },
	visualDescription: { ...type.caption, color: colors.text2 },
	visualFooter: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[2] },
  visualSite: { ...type.caption, flexShrink: 1, color: colors.text3 },
  copy: { gap: 3, padding: space[3] },
  siteRow: { flexDirection: 'row', alignItems: 'center', gap: 5 },
  site: { ...type.caption, flexShrink: 1, color: colors.text2 },
  title: { ...type.bodyMedium, color: colors.text1 },
  description: { ...type.caption, color: colors.text2 },
  copyOwn: { color: colors.onAccent },
  descriptionOwn: { color: colors.onAccent, opacity: 0.68 },
	  pressed: { opacity: 0.78, transform: [{ scale: 0.96 }] },
});
