import React, { useEffect, useMemo, useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import { Image } from 'expo-image';
import { SymbolView } from 'expo-symbols';
import * as Linking from 'expo-linking';

import { api } from '../api/client';
import type { LinkPreview } from '../api/types';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';
import { buildApiUrl, buildAuthHeaders } from '../api/requestHelpers';
import { colors, radius, space, type } from '../theme/tokens';

const previewCache = new Map<string, Promise<LinkPreview | null>>();

function previewFor(sessionToken: string, url: string): Promise<LinkPreview | null> {
  const cached = previewCache.get(url);
  if (cached) return cached;
  const request = api.linkPreview(sessionToken, url).then((result) => result.preview).catch(() => null);
  previewCache.set(url, request);
  return request;
}

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
  const [preview, setPreview] = useState<LinkPreview | null | undefined>(undefined);
  useEffect(() => {
    let active = true;
    void previewFor(sessionToken, url).then((value) => {
      if (active) setPreview(value);
    });
    return () => { active = false; };
  }, [sessionToken, url]);

  const imageSource = useMemo(() => {
    const path = preview?.imageUrl?.trim();
    if (!path) return null;
    return {
      uri: /^https?:\/\//i.test(path) ? path : buildApiUrl(API_BASE_URL, path),
      headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken, { Accept: 'image/*' }),
    };
  }, [preview?.imageUrl, sessionToken]);

  const site = preview?.siteName?.trim() || hostLabel(url);
  const title = preview?.title?.trim() || hostLabel(url);
  const description = preview?.description?.trim();

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
        onPress={() => void Linking.openURL(url).catch(() => undefined)}
        onLongPress={onLongPress}
        delayLongPress={430}
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
    return (
      <Pressable
        accessibilityRole="link"
        accessibilityLabel={`${author} on X: ${description || 'Post'}`}
        accessibilityHint="Opens this post on X"
        onPress={() => void Linking.openURL(url).catch(() => undefined)}
        onLongPress={onLongPress}
        delayLongPress={430}
        style={({ pressed }) => [
          styles.card,
          styles.postCard,
          seamless && styles.cardSeamless,
          own && (seamless ? styles.cardSeamlessOwn : styles.cardOwn),
          pressed && styles.pressed,
        ]}
      >
        <View style={styles.postHeader}>
          {imageSource ? (
            <Image source={imageSource} cachePolicy="memory-disk" contentFit="cover" recyclingKey={`${url}-avatar`} transition={160} style={styles.avatar} />
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
        <View style={styles.postFooter}>
          <Text style={[styles.postDate, own && styles.descriptionOwn]}>{preview.publishedAt ? `${preview.publishedAt} · x.com` : 'x.com'}</Text>
          <SymbolView name="arrow.up.right" tintColor={own ? colors.onAccent : colors.text3} size={10} />
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
        onPress={() => void Linking.openURL(url).catch(() => undefined)}
        onLongPress={onLongPress}
        delayLongPress={430}
        style={({ pressed }) => [
          styles.card,
          styles.visualCard,
          seamless && styles.cardSeamless,
          pressed && styles.pressed,
        ]}
      >
        <View style={styles.hero}>
          <Image
            source={imageSource}
            cachePolicy="memory-disk"
            contentFit="cover"
            recyclingKey={url}
            transition={160}
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
          <Text numberOfLines={1} style={styles.visualSite}>{site}</Text>
        </View>
      </Pressable>
    );
  }

  return (
    <Pressable
      accessibilityRole="link"
      accessibilityLabel={`${title}, ${site}`}
      accessibilityHint="Opens this link"
      onPress={() => void Linking.openURL(url).catch(() => undefined)}
      onLongPress={onLongPress}
      delayLongPress={430}
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
    minWidth: 252,
    maxWidth: 300,
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
  authorCopy: { flex: 1 },
  author: { ...type.bodyMedium, color: colors.text1 },
  handle: { ...type.caption, color: colors.text2 },
  xMark: { fontSize: 19, lineHeight: 23, color: colors.text1 },
  postText: { ...type.body, color: colors.text1 },
  postDate: { ...type.caption, color: colors.text3 },
  postFooter: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[2] },
  cardOwn: { backgroundColor: 'rgba(0,0,0,0.08)', borderColor: 'rgba(0,0,0,0.10)' },
  visualCard: { gap: 0, backgroundColor: colors.surface2 },
  hero: { width: '100%', aspectRatio: 16 / 9, overflow: 'hidden', backgroundColor: colors.surface3 },
  imageOutline: { ...StyleSheet.absoluteFill, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
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
  visualTitle: { ...type.bodyMedium, color: colors.text1 },
  visualSite: { ...type.caption, color: colors.text3 },
  copy: { gap: 3, padding: space[3] },
  siteRow: { flexDirection: 'row', alignItems: 'center', gap: 5 },
  site: { ...type.caption, flexShrink: 1, color: colors.text2 },
  title: { ...type.bodyMedium, color: colors.text1 },
  description: { ...type.caption, color: colors.text2 },
  copyOwn: { color: colors.onAccent },
  descriptionOwn: { color: 'rgba(14,14,16,0.66)' },
  pressed: { opacity: 0.78, transform: [{ scale: 0.98 }] },
});
