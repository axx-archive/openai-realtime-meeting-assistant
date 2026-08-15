import React, { useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';

import type { PrivateRiffSharePreviewResponse } from '../api/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import {
  initialPrivateRiffParagraphTokens,
  selectedPrivateRiffParagraphTokens,
} from './privateRiff';

type Props = {
  visible: boolean;
  preview: PrivateRiffSharePreviewResponse | null;
  loading: boolean;
  publishing: 'agent' | 'draft' | null;
  error?: string;
  onClose: () => void;
  onSubmit: (mode: 'agent' | 'draft', paragraphTokens: string[]) => void;
};

export function PrivateRiffShareSheet({ visible, preview, loading, publishing, error, onClose, onSubmit }: Props) {
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    if (!visible || !preview) return;
    setSelected(initialPrivateRiffParagraphTokens(preview.paragraphs));
  }, [preview?.messageId, visible]);
  const tokens = useMemo(
    () => preview ? selectedPrivateRiffParagraphTokens(preview.paragraphs, selected) : [],
    [preview, selected],
  );
  const busy = Boolean(publishing);
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.safe} edges={['top', 'bottom', 'left', 'right']}>
        <View style={styles.header}>
          <View style={styles.heading}>
            <Text style={styles.eyebrow}>SHARE FROM PRIVATE RIFF</Text>
            <Text accessibilityRole="header" style={styles.title}>Choose what becomes public</Text>
          </View>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Close share preview"
            accessibilityState={{ disabled: busy }}
            disabled={busy}
            hitSlop={8}
            onPress={onClose}
            style={({ pressed }) => [styles.close, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" size={16} tintColor={colors.text2} />
          </Pressable>
        </View>
        <ScrollView contentInsetAdjustmentBehavior="automatic" contentContainerStyle={styles.content}>
          {loading ? (
            <View accessibilityLiveRegion="polite" style={styles.loading}>
              <ActivityIndicator color={colors.emberText} />
              <Text style={styles.loadingText}>Preparing an exact preview…</Text>
            </View>
          ) : preview ? (
            <>
              <View style={styles.destination}>
                <SymbolView name="number" size={14} tintColor={colors.emberText} />
                <Text style={styles.destinationText}>Destination · #{preview.destination.title.replace(/^#/, '')}</Text>
              </View>
              <Text style={styles.help}>Only checked paragraphs will cross the private boundary. Your prompts and the rest of this Riff stay private.</Text>
              <View accessibilityRole="list" style={styles.paragraphs}>
                {preview.paragraphs.map((paragraph, index) => {
                  const checked = selected.has(paragraph.token);
                  return (
                    <Pressable
                      key={paragraph.token}
                      accessibilityRole="checkbox"
                      accessibilityLabel={`Paragraph ${index + 1}: ${paragraph.text}`}
                      accessibilityState={{ checked, disabled: busy }}
                      disabled={busy}
                      onPress={() => setSelected((current) => {
                        const next = new Set(current);
                        if (next.has(paragraph.token)) next.delete(paragraph.token);
                        else next.add(paragraph.token);
                        return next;
                      })}
                      style={({ pressed }) => [styles.paragraph, checked && styles.paragraphSelected, pressed && styles.pressed]}
                    >
                      <SymbolView name={checked ? 'checkmark.circle.fill' : 'circle'} size={20} tintColor={checked ? colors.emberText : colors.text3} />
                      <Text style={styles.paragraphText}>{paragraph.text}</Text>
                    </Pressable>
                  );
                })}
              </View>
            </>
          ) : null}
          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
        </ScrollView>
        <View style={styles.footer}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Use selected paragraphs in my message"
            accessibilityHint="Opens the source channel with an editable draft. Nothing posts yet."
            accessibilityState={{ disabled: loading || busy || tokens.length === 0 }}
            disabled={loading || busy || tokens.length === 0}
            onPress={() => onSubmit('draft', tokens)}
            style={({ pressed }) => [styles.secondary, pressed && styles.pressed, (loading || busy || tokens.length === 0) && styles.disabled]}
          >
            {publishing === 'draft' ? <ActivityIndicator color={colors.text1} size="small" /> : <SymbolView name="square.and.pencil" size={16} tintColor={colors.text1} />}
            <Text style={styles.secondaryText}>{publishing === 'draft' ? 'Preparing…' : 'Use in my message'}</Text>
          </Pressable>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Share selected paragraphs as the agent"
            accessibilityHint="Posts only the selected text to the source channel with your authorization and Private Riff provenance."
            accessibilityState={{ disabled: loading || busy || tokens.length === 0 }}
            disabled={loading || busy || tokens.length === 0}
            onPress={() => onSubmit('agent', tokens)}
            style={({ pressed }) => [styles.primary, pressed && styles.pressed, (loading || busy || tokens.length === 0) && styles.disabled]}
          >
            {publishing === 'agent' ? <ActivityIndicator color={colors.onAccent} size="small" /> : <SymbolView name="paperplane.fill" size={16} tintColor={colors.onAccent} />}
            <Text style={styles.primaryText}>{publishing === 'agent' ? 'Sharing…' : 'Share agent answer'}</Text>
          </Pressable>
        </View>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  header: { minHeight: 62, flexDirection: 'row', alignItems: 'center', paddingHorizontal: space[4], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  heading: { flex: 1, minWidth: 0 },
  eyebrow: { ...type.label, color: colors.emberText },
  title: { ...type.headline, color: colors.text1 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  content: { gap: space[4], padding: space[5] },
  loading: { minHeight: 160, alignItems: 'center', justifyContent: 'center', gap: space[3] },
  loadingText: { ...type.body, color: colors.text2 },
  destination: { minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[3], borderRadius: radius.full, backgroundColor: colors.emberSoft },
  destinationText: { ...type.bodyMedium, flex: 1, color: colors.text1 },
  help: { ...type.bodySm, color: colors.text2 },
  paragraphs: { gap: space[2] },
  paragraph: { minHeight: 64, flexDirection: 'row', alignItems: 'flex-start', gap: space[3], padding: space[4], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface1 },
  paragraphSelected: { borderColor: colors.ember, backgroundColor: colors.emberSoft },
  paragraphText: { ...type.body, flex: 1, color: colors.text1 },
  error: { ...type.bodySm, color: colors.danger },
  footer: { gap: space[2], padding: space[4], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  secondary: { minHeight: 50, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], borderRadius: radius.full, backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2 },
  secondaryText: { ...type.button, color: colors.text1 },
  primary: { minHeight: 52, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], borderRadius: radius.full, backgroundColor: colors.accent },
  primaryText: { ...type.button, color: colors.onAccent },
  pressed: { opacity: 0.8, transform: [{ scale: 0.98 }] },
  disabled: { opacity: 0.38 },
});
