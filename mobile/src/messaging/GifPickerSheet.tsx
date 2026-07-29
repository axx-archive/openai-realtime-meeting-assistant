import React, { useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  FlatList,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { Image } from 'expo-image';
import { SymbolView } from 'expo-symbols';

import { api, BonfireApiError } from '../api/client';
import type { GiphySearchResult } from '../api/types';
import { duration, ease, useReduceMotion } from '../theme/motion';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';

type Props = {
  visible: boolean;
  sessionToken: string;
  onClose: () => void;
  onSelect: (gif: GiphySearchResult) => Promise<boolean>;
};

export function GifPickerSheet({ visible, sessionToken, onClose, onSelect }: Props) {
  const reduced = useReduceMotion();
  const progress = useRef(new Animated.Value(0)).current;
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<GiphySearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [selecting, setSelecting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!visible) {
      progress.setValue(0);
      setQuery('');
      setResults([]);
      setError(null);
      setSelecting(null);
      return;
    }
    Animated.timing(progress, {
      toValue: 1,
      duration: reduced ? 0 : duration.med,
      easing: ease,
      useNativeDriver: true,
    }).start();
  }, [progress, reduced, visible]);

  useEffect(() => {
    if (!visible || !sessionToken) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setLoading(true);
      setError(null);
      void api.searchGiphy(sessionToken, query, controller.signal)
        .then((response) => setResults(response.results ?? []))
        .catch((caught) => {
          if (controller.signal.aborted) return;
          setResults([]);
          setError(
            caught instanceof BonfireApiError && caught.status === 503
              ? 'GIF search isn’t configured yet.'
              : 'GIF search is unavailable right now.',
          );
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }, query.trim() ? 280 : 0);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [query, sessionToken, visible]);

  async function select(gif: GiphySearchResult) {
    if (selecting) return;
    setSelecting(gif.id);
    setError(null);
    try {
      if (await onSelect(gif)) onClose();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Could not add that GIF.');
    } finally {
      setSelecting(null);
    }
  }

  return (
    <Modal visible={visible} transparent animationType="none" onRequestClose={onClose}>
      <KeyboardAvoidingView style={styles.modal} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        <Pressable accessibilityLabel="Close GIF search" onPress={onClose} style={StyleSheet.absoluteFill}>
          <Animated.View style={[StyleSheet.absoluteFill, styles.scrim, { opacity: progress }]} />
        </Pressable>
        <Animated.View
          style={[
            styles.sheet,
            {
              opacity: progress,
              transform: [{ translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [22, 0] }) }],
            },
          ]}
        >
          <View style={styles.grabber} />
          <View style={styles.header}>
            <View>
              <Text style={styles.title}>Find a GIF</Text>
              <Text style={styles.powered}>Powered by GIPHY</Text>
            </View>
            <Pressable accessibilityRole="button" accessibilityLabel="Close GIF search" onPress={onClose} style={({ pressed }) => [styles.close, pressed && styles.pressed]}>
              <SymbolView name="xmark" tintColor={colors.text2} size={14} />
            </Pressable>
          </View>
          <View style={styles.search}>
            <SymbolView name="magnifyingglass" tintColor={colors.text3} size={15} />
            <TextInput
              accessibilityLabel="Search GIFs"
              autoCapitalize="none"
              autoCorrect={false}
              clearButtonMode="while-editing"
              placeholder="Search reactions, moments, moods"
              placeholderTextColor={colors.text3}
              value={query}
              onChangeText={setQuery}
              style={styles.searchInput}
            />
          </View>
          {error ? (
            <View style={styles.empty}>
              <View style={styles.emptyIcon}><SymbolView name="sparkles" tintColor={colors.emberText} size={22} /></View>
              <Text style={styles.emptyTitle}>{error}</Text>
              <Text style={styles.emptyDetail}>Photos and files are still available from the add menu.</Text>
            </View>
          ) : (
            <FlatList
              style={styles.list}
              data={results}
              keyExtractor={(item) => item.id}
              numColumns={2}
              keyboardShouldPersistTaps="handled"
              contentContainerStyle={styles.grid}
              columnWrapperStyle={styles.row}
              ListEmptyComponent={loading ? (
                <ActivityIndicator color={colors.text2} style={styles.loader} />
              ) : (
                <Text style={styles.noResults}>{query.trim() ? 'No GIFs found.' : 'No trending GIFs right now.'}</Text>
              )}
              renderItem={({ item }) => (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel={`Add ${item.title || 'GIF'}`}
                  disabled={Boolean(selecting)}
                  onPress={() => void select(item)}
                  style={({ pressed }) => [styles.gif, pressed && styles.pressed, selecting === item.id && styles.gifSelecting]}
                >
                  <Image source={item.previewUrl} cachePolicy="memory-disk" contentFit="cover" style={StyleSheet.absoluteFill} />
                  {selecting === item.id ? (
                    <View style={styles.selecting}><ActivityIndicator color="#FFFFFF" /></View>
                  ) : null}
                </Pressable>
              )}
            />
          )}
        </Animated.View>
      </KeyboardAvoidingView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  modal: { flex: 1, justifyContent: 'flex-end' },
  scrim: { backgroundColor: colors.scrim },
  sheet: {
    ...shadow.glass,
    height: '72%',
    margin: space[2],
    paddingTop: 7,
    overflow: 'hidden',
    borderRadius: radius.xl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface1,
  },
  grabber: { alignSelf: 'center', width: 34, height: 4, borderRadius: 2, backgroundColor: colors.line2 },
  header: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingHorizontal: space[4], paddingTop: space[3] },
  title: { ...type.headline, color: colors.text1 },
  powered: { ...type.captionMedium, marginTop: 1, color: colors.emberText },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: hitMin / 2, backgroundColor: colors.surface3 },
  search: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', gap: space[2], marginHorizontal: space[4], marginTop: space[3], paddingHorizontal: space[3], borderRadius: radius.md, backgroundColor: colors.surface3 },
  searchInput: { ...type.body, flex: 1, color: colors.text1, paddingVertical: 0 },
  grid: { padding: space[3], paddingBottom: space[6] },
  list: { flex: 1 },
  row: { gap: space[2], marginBottom: space[2] },
  gif: { flex: 1, height: 132, overflow: 'hidden', borderRadius: radius.md, backgroundColor: colors.surface3 },
  gifSelecting: { opacity: 0.72 },
  selecting: { position: 'absolute', inset: 0, alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(0,0,0,0.28)' },
  loader: { paddingVertical: space[10] },
  noResults: { ...type.body, paddingVertical: space[10], color: colors.text2, textAlign: 'center' },
  empty: { flex: 1, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[8] },
  emptyIcon: { width: 52, height: 52, alignItems: 'center', justifyContent: 'center', borderRadius: 18, backgroundColor: colors.emberSoft },
  emptyTitle: { ...type.bodyMedium, marginTop: space[3], color: colors.text1, textAlign: 'center' },
  emptyDetail: { ...type.caption, marginTop: space[1], color: colors.text2, textAlign: 'center' },
  pressed: { opacity: 0.72, transform: [{ scale: 0.98 }] },
});
