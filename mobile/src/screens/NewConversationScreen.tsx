import React, { useCallback, useRef, useState } from 'react';
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { api, BonfireApiError } from '../api/client';
import type { RootStackParamList } from '../navigation/types';
import {
  newConversationAttempt,
  newConversationBody,
  type NewConversationAttempt,
  type NewConversationKind,
} from '../conversations/newConversation';
import { colors, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'NewConversation'>;

const kinds: Array<{
  id: NewConversationKind;
  label: string;
  detail: string;
  icon: 'bubble.left.fill' | 'number';
}> = [
  { id: 'private', label: 'Private chat', detail: 'Only you and Scout', icon: 'bubble.left.fill' },
  { id: 'channel', label: 'Channel', detail: 'Visible to everyone in Bonfire', icon: 'number' },
];

export function NewConversationScreen({ navigation }: Props) {
  const { sessionToken } = useAuth();
  const [kind, setKind] = useState<NewConversationKind>('private');
  const [title, setTitle] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const attemptRef = useRef<NewConversationAttempt | null>(null);

  const create = useCallback(async () => {
    if (!sessionToken || saving) return;
    const attempt = newConversationAttempt(attemptRef.current, kind, title);
    if (!attempt) {
      setError(title.trim() ? 'Keep the name under 80 characters.' : 'Name this conversation first.');
      return;
    }
    attemptRef.current = attempt;
    setSaving(true);
    setError(null);
    try {
      const response = await api.createScoutThread(sessionToken, newConversationBody(attempt));
      const threadId = String(response.thread?.id ?? '').trim();
      if (!threadId) throw new Error('The conversation was not created.');
      attemptRef.current = null;
      navigation.navigate('Thread', {
        threadId,
        title: attempt.kind === 'channel' ? `#${attempt.title.replace(/^#/, '')}` : attempt.title,
      });
    } catch (cause) {
      setError(cause instanceof BonfireApiError ? cause.message : 'Could not create this conversation. Try again.');
    } finally {
      setSaving(false);
    }
  }, [kind, navigation, saving, sessionToken, title]);

  const selectKind = useCallback((next: NewConversationKind) => {
    if (saving || next === kind) return;
    setKind(next);
    attemptRef.current = null;
    setError(null);
  }, [kind, saving]);

  const valid = Boolean(title.replace(/\s+/g, ' ').trim()) && title.replace(/\s+/g, ' ').trim().length <= 80;
  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
      <KeyboardAvoidingView behavior={Platform.OS === 'ios' ? 'padding' : undefined} style={styles.keyboard}>
        <View style={styles.header}>
          <Pressable accessibilityLabel="Cancel" accessibilityRole="button" onPress={() => navigation.goBack()} style={({ pressed }) => [styles.headerAction, pressed && styles.pressed]}>
            <Text style={styles.cancel}>Cancel</Text>
          </Pressable>
          <Text accessibilityRole="header" style={styles.headerTitle}>New conversation</Text>
          <View style={styles.headerAction} />
        </View>

        <View style={styles.body}>
          <View accessibilityRole="tablist" style={styles.kindList}>
            {kinds.map((item) => {
              const selected = kind === item.id;
              return (
                <Pressable
                  key={item.id}
                  accessibilityLabel={item.label}
                  accessibilityHint={item.detail}
                  accessibilityRole="tab"
                  accessibilityState={{ selected }}
                  onPress={() => selectKind(item.id)}
                  style={({ pressed }) => [styles.kind, selected && styles.kindSelected, pressed && styles.pressed]}
                >
                  <View style={[styles.kindIcon, selected && styles.kindIconSelected]}>
                    <SymbolView name={item.icon} size={19} tintColor={selected ? colors.ember : colors.text2} />
                  </View>
                  <View style={styles.kindCopy}>
                    <Text style={[styles.kindLabel, selected && styles.kindLabelSelected]}>{item.label}</Text>
                    <Text style={styles.kindDetail}>{item.detail}</Text>
                  </View>
                  {selected ? <SymbolView name="checkmark.circle.fill" size={19} tintColor={colors.ember} /> : null}
                </Pressable>
              );
            })}
          </View>

          <View style={styles.fieldGroup}>
            <Text style={styles.fieldLabel}>{kind === 'channel' ? 'Channel name' : 'Chat name'}</Text>
            <View style={styles.field}>
              {kind === 'channel' ? <Text style={styles.prefix}>#</Text> : null}
              <TextInput
                accessibilityLabel={kind === 'channel' ? 'Channel name' : 'Private chat name'}
                autoCapitalize="sentences"
                autoCorrect
                autoFocus
                editable={!saving}
                enterKeyHint="done"
                maxLength={80}
                onChangeText={(value) => {
                  setTitle(value);
                  if (attemptRef.current?.title !== value.replace(/\s+/g, ' ').trim()) attemptRef.current = null;
                  if (error) setError(null);
                }}
                onSubmitEditing={() => { void create(); }}
                placeholder={kind === 'channel' ? 'venture-review' : 'Investor research'}
                placeholderTextColor={colors.text3}
                returnKeyType="done"
                style={styles.input}
                value={title}
              />
            </View>
            <Text style={styles.fieldHelp}>
              {kind === 'channel'
                ? 'Everyone on the platform can find and join this channel.'
                : 'This stays private to your account and Scout.'}
            </Text>
          </View>

          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
          <Pressable
            accessibilityLabel={kind === 'channel' ? 'Create channel' : 'Create private chat'}
            accessibilityRole="button"
            accessibilityState={{ disabled: !valid || saving }}
            disabled={!valid || saving}
            onPress={() => { void create(); }}
            style={({ pressed }) => [styles.create, (!valid || saving) && styles.createDisabled, pressed && styles.pressed]}
          >
            {saving ? <ActivityIndicator color={colors.onAccent} /> : <Text style={styles.createLabel}>{kind === 'channel' ? 'Create channel' : 'Start private chat'}</Text>}
          </Pressable>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  keyboard: { flex: 1 },
  header: { minHeight: 56, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingHorizontal: space[3] },
  headerAction: { width: 72, minHeight: 44, justifyContent: 'center' },
  cancel: { ...type.body, color: colors.text2 },
  headerTitle: { ...type.bodyMedium, color: colors.text1, textAlign: 'center' },
  body: { width: '100%', maxWidth: 620, alignSelf: 'center', flex: 1, gap: space[6], paddingHorizontal: space[5], paddingTop: space[4], paddingBottom: space[5] },
  kindList: { gap: space[2] },
  kind: { minHeight: 76, flexDirection: 'row', alignItems: 'center', gap: space[3], padding: space[3], borderRadius: radius.lg, borderCurve: 'continuous', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.border, backgroundColor: colors.surface1 },
  kindSelected: { borderColor: colors.ember, backgroundColor: colors.accentSoft },
  kindIcon: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.surface3 },
  kindIconSelected: { backgroundColor: colors.surface1 },
  kindCopy: { flex: 1, minWidth: 0, gap: 2 },
  kindLabel: { ...type.bodyMedium, color: colors.text1 },
  kindLabelSelected: { color: colors.emberText },
  kindDetail: { ...type.bodySm, color: colors.text2 },
  fieldGroup: { gap: space[2] },
  fieldLabel: { ...type.label, color: colors.text2, textTransform: 'uppercase' },
  field: { minHeight: 54, flexDirection: 'row', alignItems: 'center', gap: space[1], paddingHorizontal: space[4], borderRadius: radius.lg, borderCurve: 'continuous', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface1 },
  prefix: { ...type.title2, color: colors.text3 },
  input: { ...type.body, flex: 1, minWidth: 0, minHeight: 52, color: colors.text1 },
  fieldHelp: { ...type.bodySm, color: colors.text2 },
  error: { ...type.bodySm, color: colors.danger, textAlign: 'center' },
  create: { minHeight: 52, alignItems: 'center', justifyContent: 'center', marginTop: 'auto', borderRadius: radius.full, borderCurve: 'continuous', backgroundColor: colors.accent },
  createDisabled: { opacity: 0.34 },
  createLabel: { ...type.button, color: colors.onAccent },
  pressed: { opacity: 0.82, transform: [{ scale: 0.98 }] },
});
