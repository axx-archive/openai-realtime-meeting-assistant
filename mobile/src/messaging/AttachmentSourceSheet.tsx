import React, { useEffect, useRef } from 'react';
import { Animated, Modal, Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';

import { duration, ease, useReduceMotion } from '../theme/motion';
import { colors, radius, shadow, space, type } from '../theme/tokens';

type Props = {
  visible: boolean;
  onClose: () => void;
  onFiles: () => void;
  onPhotos: () => void;
  onGifs: () => void;
};

const sources: Array<{
  key: 'files' | 'photos' | 'gifs';
  title: string;
  detail: string;
  icon: SFSymbol;
}> = [
  { key: 'photos', title: 'Photos', detail: 'Camera Roll', icon: 'photo.on.rectangle.angled' },
  { key: 'files', title: 'Files', detail: 'Images or PDF', icon: 'folder.fill' },
  { key: 'gifs', title: 'GIFs', detail: 'Search GIPHY', icon: 'sparkles' },
];

export function AttachmentSourceSheet({ visible, onClose, onFiles, onPhotos, onGifs }: Props) {
  const reduced = useReduceMotion();
  const progress = useRef(new Animated.Value(0)).current;
  const pendingSource = useRef<(typeof sources)[number]['key'] | null>(null);
  useEffect(() => {
    if (!visible) {
      progress.setValue(0);
      return;
    }
    Animated.timing(progress, {
      toValue: 1,
      duration: reduced ? 0 : duration.med,
      easing: ease,
      useNativeDriver: true,
    }).start();
  }, [progress, reduced, visible]);

  const close = () => {
    pendingSource.current = null;
    onClose();
  };

  const choose = (source: (typeof sources)[number]['key']) => {
    // iOS cannot present Photos or Files while this modal is still dismissing.
    // Hold the intent and hand off only after the source sheet is fully gone.
    pendingSource.current = source;
    onClose();
  };

  const handOff = () => {
    const source = pendingSource.current;
    pendingSource.current = null;
    if (!source) return;
    if (source === 'photos') onPhotos();
    else if (source === 'files') onFiles();
    else onGifs();
  };

  return (
    <Modal visible={visible} transparent animationType="none" onDismiss={handOff} onRequestClose={close}>
      <View style={styles.modal}>
        <Pressable accessibilityLabel="Close attachment options" onPress={close} style={StyleSheet.absoluteFill}>
          <Animated.View style={[StyleSheet.absoluteFill, styles.scrim, { opacity: progress }]} />
        </Pressable>
        <Animated.View
          style={[
            styles.sheet,
            {
              opacity: progress,
              transform: [
                { translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [18, 0] }) },
                { scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.98, 1] }) },
              ],
            },
          ]}
        >
          <View style={styles.grabber} />
          <Text style={styles.title}>Add to message</Text>
          <View style={styles.options}>
            {sources.map((source) => (
              <Pressable
                key={source.key}
                accessibilityRole="button"
                accessibilityLabel={`${source.title}, ${source.detail}`}
                onPress={() => choose(source.key)}
                style={({ pressed }) => [styles.option, pressed && styles.optionPressed]}
              >
                <View style={[styles.icon, source.key === 'gifs' && styles.gifIcon]}>
                  <SymbolView
                    name={source.icon}
                    tintColor={source.key === 'gifs' ? colors.emberText : colors.text1}
                    size={22}
                  />
                </View>
                <Text style={styles.optionTitle}>{source.title}</Text>
                <Text style={styles.optionDetail}>{source.detail}</Text>
              </Pressable>
            ))}
          </View>
        </Animated.View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  modal: { flex: 1, justifyContent: 'flex-end' },
  scrim: { backgroundColor: colors.scrim },
  sheet: {
    ...shadow.glass,
    margin: space[3],
    paddingHorizontal: space[4],
    paddingTop: 7,
    paddingBottom: space[5],
    borderRadius: radius.xl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface1,
  },
  grabber: { alignSelf: 'center', width: 34, height: 4, borderRadius: 2, backgroundColor: colors.line2 },
  title: { ...type.headline, marginTop: space[3], marginBottom: space[4], color: colors.text1 },
  options: { flexDirection: 'row', gap: space[2] },
  option: {
    flex: 1,
    minHeight: 128,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: space[2],
    borderRadius: radius.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface3,
  },
  optionPressed: { opacity: 0.72, transform: [{ scale: 0.97 }] },
  icon: { width: 46, height: 46, alignItems: 'center', justifyContent: 'center', borderRadius: 16, backgroundColor: colors.accentSoft },
  gifIcon: { backgroundColor: colors.emberSoft },
  optionTitle: { ...type.bodyMedium, marginTop: space[2], color: colors.text1 },
  optionDetail: { ...type.caption, marginTop: 2, color: colors.text2, textAlign: 'center' },
});
