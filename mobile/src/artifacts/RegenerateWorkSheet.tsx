import React, { useEffect, useState } from 'react';
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';

import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Props = {
  visible: boolean;
  agentName: string;
  initialPrompt: string;
  busy: boolean;
  error?: string;
  onClose: () => void;
  onSubmit: (prompt: string) => void;
};

export function RegenerateWorkSheet({ visible, agentName, initialPrompt, busy, error, onClose, onSubmit }: Props) {
  const [prompt, setPrompt] = useState(initialPrompt);

  useEffect(() => {
    if (visible) setPrompt(initialPrompt);
  }, [initialPrompt, visible]);

  const cleanPrompt = prompt.trim();
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="formSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.safe} edges={['left', 'right', 'bottom']}>
        <KeyboardAvoidingView style={styles.fill} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
          <View style={styles.handle} />
          <View style={styles.header}>
            <View style={styles.headerCopy}>
              <Text style={styles.eyebrow}>REGENERATE DELIVERABLE</Text>
              <Text accessibilityRole="header" style={styles.title}>Give {agentName} a sharper brief</Text>
            </View>
            <Pressable accessibilityRole="button" accessibilityLabel="Close regenerate form" disabled={busy} onPress={onClose} style={({ pressed }) => [styles.close, pressed && styles.pressed]}>
              <SymbolView name="xmark" tintColor={colors.text2} size={15} />
            </Pressable>
          </View>
          <View style={styles.content}>
            <Text style={styles.guidance}>Edit the request below. The existing deliverable stays available until the replacement finishes.</Text>
            <TextInput
              accessibilityLabel="Regeneration prompt"
              autoFocus
              editable={!busy}
              multiline
              onChangeText={setPrompt}
              placeholder="What should change?"
              placeholderTextColor={colors.text3}
              selectionColor={colors.info}
              style={styles.input}
              textAlignVertical="top"
              value={prompt}
            />
            {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Regenerate deliverable"
              accessibilityState={{ disabled: busy || !cleanPrompt }}
              disabled={busy || !cleanPrompt}
              onPress={() => onSubmit(cleanPrompt)}
              style={({ pressed }) => [styles.primary, pressed && styles.pressed, (busy || !cleanPrompt) && styles.disabled]}
            >
              {busy ? <ActivityIndicator color={colors.onAccent} size="small" /> : <SymbolView name="arrow.clockwise" tintColor={colors.onAccent} size={17} />}
              <Text style={styles.primaryText}>{busy ? 'Starting…' : 'Regenerate'}</Text>
            </Pressable>
          </View>
        </KeyboardAvoidingView>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  fill: { flex: 1 },
  handle: { alignSelf: 'center', width: 36, height: 5, marginTop: space[2], borderRadius: radius.full, backgroundColor: colors.line2 },
  header: { minHeight: 82, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[5], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  headerCopy: { minWidth: 0, flex: 1, gap: 2 },
  eyebrow: { ...type.label, color: colors.emberText, letterSpacing: 0.6 },
  title: { ...type.title2, color: colors.text1 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, borderCurve: 'continuous', backgroundColor: colors.surface3 },
  content: { flex: 1, gap: space[4], padding: space[5] },
  guidance: { ...type.bodySm, color: colors.text2 },
  input: { ...type.body, minHeight: 180, maxHeight: 360, padding: space[4], borderRadius: radius.xl, borderCurve: 'continuous', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, color: colors.text1, backgroundColor: colors.surface1 },
  error: { ...type.caption, color: colors.danger },
  primary: { minHeight: 52, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], borderRadius: radius.full, backgroundColor: colors.accent },
  primaryText: { ...type.captionMedium, color: colors.onAccent },
  pressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  disabled: { opacity: 0.46 },
});
