import React, { useEffect, useRef, useState } from 'react';
import {
  AccessibilityInfo,
  ActivityIndicator,
  findNodeHandle,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';

import type { ProjectCorrectionProjection } from '../api/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Selection = { kind: 'project' | 'remove'; token: string; title: string };

type Props = {
  visible: boolean;
  contained?: boolean;
  projection: ProjectCorrectionProjection | null;
  loading: boolean;
  updating: boolean;
  error: string;
  returnFocusHandle?: number;
  onClose: () => void;
  onReload: () => void;
  onSubmit: (selection: Selection) => void;
};

export function ProjectCorrectionSheet({
  visible,
  contained = false,
  projection,
  loading,
  updating,
  error,
  returnFocusHandle,
  onClose,
  onReload,
  onSubmit,
}: Props) {
  const [selection, setSelection] = useState<Selection | null>(null);
  const titleRef = useRef<Text>(null);
  const projectionKey = `${projection?.scopeKey ?? ''}:${projection?.current.contextRevision ?? 0}`;

  useEffect(() => {
    setSelection(null);
  }, [projectionKey, visible]);

  useEffect(() => {
    if (!visible) return;
    const timer = setTimeout(() => {
      const handle = findNodeHandle(titleRef.current);
      if (handle) AccessibilityInfo.setAccessibilityFocus(handle);
    }, 120);
    return () => clearTimeout(timer);
  }, [visible, projectionKey]);

  const close = () => {
    if (updating) return;
    onClose();
    if (returnFocusHandle) {
      setTimeout(() => AccessibilityInfo.setAccessibilityFocus(returnFocusHandle), 120);
    }
  };

  const content = (
    <SafeAreaView style={[styles.surface, contained && styles.containedSurface]}>
      <View style={styles.header}>
        <View style={styles.headerCopy}>
          <Text ref={titleRef} accessible accessibilityRole="header" style={styles.title}>Change project</Text>
          <Text style={styles.subtitle}>This changes only this message.</Text>
        </View>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Close project correction"
          disabled={updating}
          onPress={close}
          style={({ pressed }) => [styles.close, pressed && styles.pressed, updating && styles.disabled]}
        >
          <SymbolView name="xmark" tintColor={colors.text1} size={17} />
        </Pressable>
      </View>

      {loading ? (
        <View accessibilityLabel="Loading project choices" style={styles.center}>
          <ActivityIndicator color={colors.ember} />
          <Text style={styles.supporting}>Checking the current project…</Text>
        </View>
      ) : projection ? (
        <>
          <View style={styles.currentCard}>
            <Text style={styles.eyebrow}>Current</Text>
            <Text style={styles.currentTitle}>{projection.current.title || 'No project'}</Text>
            {projection.current.status === 'unavailable' ? (
              <Text style={styles.warning}>This Project is no longer available. Choose an authorized Project or remove the link.</Text>
            ) : null}
          </View>

          <ScrollView
            contentContainerStyle={styles.choices}
            showsVerticalScrollIndicator={false}
          >
            {projection.choices.map((choice) => {
              const selected = selection?.token === choice.token;
              return (
                <Pressable
                  key={choice.token}
                  accessibilityRole="radio"
                  accessibilityState={{ selected, disabled: updating }}
                  disabled={updating}
                  onPress={() => setSelection({ kind: 'project', token: choice.token, title: choice.title })}
                  style={({ pressed }) => [styles.choice, selected && styles.choiceSelected, pressed && styles.pressed]}
                >
                  <Text style={styles.choiceText}>{choice.title}</Text>
                  <View style={[styles.radio, selected && styles.radioSelected]}>
                    {selected ? <View style={styles.radioDot} /> : null}
                  </View>
                </Pressable>
              );
            })}
            {projection.remove?.token ? (
              <Pressable
                accessibilityRole="radio"
                accessibilityLabel={projection.remove.title || 'No project'}
                accessibilityState={{ selected: selection?.kind === 'remove', disabled: updating }}
                disabled={updating}
                onPress={() => setSelection({ kind: 'remove', token: projection.remove!.token, title: projection.remove!.title || 'No project' })}
                style={({ pressed }) => [styles.choice, selection?.kind === 'remove' && styles.choiceSelected, pressed && styles.pressed]}
              >
                <Text style={styles.choiceText}>{projection.remove.title || 'No project'}</Text>
                <View style={[styles.radio, selection?.kind === 'remove' && styles.radioSelected]}>
                  {selection?.kind === 'remove' ? <View style={styles.radioDot} /> : null}
                </View>
              </Pressable>
            ) : null}
          </ScrollView>

          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
          <View style={styles.actions}>
            <Pressable
              accessibilityRole="button"
              disabled={updating}
              onPress={close}
              style={({ pressed }) => [styles.cancel, pressed && styles.pressed, updating && styles.disabled]}
            >
              <Text style={styles.cancelText}>Cancel</Text>
            </Pressable>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={selection?.kind === 'remove' ? 'Remove project from message' : 'Update project for message'}
              disabled={!selection || updating}
              onPress={() => selection && onSubmit(selection)}
              style={({ pressed }) => [styles.update, pressed && styles.pressed, (!selection || updating) && styles.disabled]}
            >
              {updating ? <ActivityIndicator color={colors.onAccent} /> : null}
              <Text style={styles.updateText}>{selection?.kind === 'remove' ? 'Remove' : 'Update project'}</Text>
            </Pressable>
          </View>
        </>
      ) : (
        <View style={styles.center}>
          <Text accessibilityRole="alert" style={styles.error}>{error || 'Project choices are not available.'}</Text>
          <Pressable accessibilityRole="button" onPress={onReload} style={({ pressed }) => [styles.update, pressed && styles.pressed]}>
            <Text style={styles.updateText}>Try again</Text>
          </Pressable>
        </View>
      )}
    </SafeAreaView>
  );

  if (contained) return visible ? content : null;
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={close}>
      {content}
    </Modal>
  );
}

const styles = StyleSheet.create({
  surface: { flex: 1, paddingHorizontal: space[5], backgroundColor: colors.surface1 },
  containedSurface: { position: 'absolute', inset: 0, zIndex: 110, elevation: 110 },
  header: { minHeight: 72, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[3] },
  headerCopy: { flex: 1, minWidth: 0 },
  title: { ...type.title2, color: colors.text1 },
  subtitle: { ...type.caption, color: colors.text3, marginTop: 2 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface2 },
  center: { flex: 1, alignItems: 'center', justifyContent: 'center', gap: space[3], paddingVertical: space[8] },
  supporting: { ...type.body, color: colors.text2, textAlign: 'center' },
  currentCard: { gap: 3, padding: space[4], marginBottom: space[3], borderRadius: radius.lg, backgroundColor: colors.surface2 },
  eyebrow: { ...type.label, color: colors.text3, textTransform: 'uppercase' },
  currentTitle: { ...type.bodyMedium, color: colors.text1 },
  warning: { ...type.caption, color: colors.text2, marginTop: space[1] },
  choices: { gap: space[2], paddingBottom: space[4] },
  choice: { minHeight: 54, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[3], paddingHorizontal: space[4], paddingVertical: space[2], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2 },
  choiceSelected: { borderColor: colors.ember, backgroundColor: colors.emberSoft },
  choiceText: { ...type.body, flex: 1, color: colors.text1 },
  radio: { width: 22, height: 22, alignItems: 'center', justifyContent: 'center', borderRadius: 11, borderWidth: 1.5, borderColor: colors.line2 },
  radioSelected: { borderColor: colors.ember },
  radioDot: { width: 10, height: 10, borderRadius: 5, backgroundColor: colors.ember },
  error: { ...type.caption, color: colors.danger, textAlign: 'center', marginVertical: space[2] },
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2], paddingVertical: space[4] },
  cancel: { minHeight: hitMin, flexGrow: 1, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[4], borderRadius: radius.full, backgroundColor: colors.surface2 },
  cancelText: { ...type.bodyMedium, color: colors.text1 },
  update: { minHeight: hitMin, flexGrow: 1, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], paddingHorizontal: space[4], borderRadius: radius.full, backgroundColor: colors.accent },
  updateText: { ...type.bodyMedium, color: colors.onAccent },
  pressed: { opacity: 0.72 },
  disabled: { opacity: 0.48 },
});
