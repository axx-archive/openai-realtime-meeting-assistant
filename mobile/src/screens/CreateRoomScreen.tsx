import React, { useState } from 'react';
import { Pressable, ScrollView, StyleSheet, Switch, Text, TextInput, View } from 'react-native';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';
import * as Haptics from 'expo-haptics';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Screen } from '../components/Screen';
import type { RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'CreateRoom'>;

export function CreateRoomScreen({ navigation, route }: Props) {
  const { sessionToken } = useAuth();
  const displayMode = route.params?.displayMode ?? 'sheet';
  const isWorkstation = displayMode === 'workstation';
  const [name, setName] = useState('');
  const [passcode, setPasscode] = useState('');
  const [guestAccess, setGuestAccess] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function create() {
    if (!sessionToken || !name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.createRoom(sessionToken, { name: name.trim(), passcode: passcode.trim() || undefined, guestAccess });
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      navigation.replace('Room', { roomId: result.room.id, title: result.room.name });
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not create the room.');
    } finally {
      setBusy(false);
    }
  }

  // Workstation mode: web-class layout with rail visible
  if (isWorkstation) {
    return (
      <SafeAreaView style={styles.workstationSafe} edges={['top', 'bottom']}>
        {/* Workstation header: back arrow + title */}
        <View style={styles.workstationHeader}>
          <Pressable
            accessibilityLabel="Back"
            accessibilityRole="button"
            onPress={() => navigation.goBack()}
            style={({ pressed }) => [styles.workstationBack, pressed && styles.pressed]}
          >
            <SymbolView name="chevron.left" size={20} tintColor={colors.text1} />
          </Pressable>
          <View style={styles.workstationHeaderText}>
            <Text accessibilityRole="header" style={styles.workstationTitle}>New room</Text>
            <Text style={styles.workstationSubtitle}>A shared space for a team, project, or moment</Text>
          </View>
        </View>

        <ScrollView
          contentContainerStyle={styles.workstationScroll}
          keyboardShouldPersistTaps="handled"
          showsVerticalScrollIndicator={false}
        >
          <View style={styles.workstationContent}>
            {error ? <Text accessibilityRole="alert" style={styles.workstationError}>{error}</Text> : null}

            <View style={styles.workstationCard}>
              <View style={styles.workstationFieldGroup}>
                <Text style={styles.workstationLabel}>Room name</Text>
                <TextInput
                  value={name}
                  onChangeText={setName}
                  placeholder="Monday standup"
                  placeholderTextColor={colors.text3}
                  style={styles.workstationInput}
                  autoFocus
                />
              </View>

              <View style={styles.workstationFieldGroup}>
                <Text style={styles.workstationLabel}>Passcode · optional</Text>
                <TextInput
                  value={passcode}
                  onChangeText={setPasscode}
                  placeholder="Leave blank for one-tap access"
                  placeholderTextColor={colors.text3}
                  style={styles.workstationInput}
                  secureTextEntry
                />
              </View>

              <View style={styles.workstationSwitchRow}>
                <View style={styles.workstationSwitchCopy}>
                  <Text style={styles.workstationSwitchTitle}>Guest access</Text>
                  <Text style={styles.workstationSwitchSubtitle}>Allow a revocable guest link for this room.</Text>
                </View>
                <Switch value={guestAccess} onValueChange={setGuestAccess} />
              </View>
            </View>

            <Pressable
              accessibilityRole="button"
              onPress={create}
              disabled={!name.trim() || busy}
              style={({ pressed }) => [
                styles.workstationCreate,
                pressed && styles.pressed,
                (!name.trim() || busy) && styles.workstationDisabled,
              ]}
            >
              <Text style={styles.workstationCreateText}>{busy ? 'Creating…' : 'Create room'}</Text>
            </Pressable>
          </View>
        </ScrollView>
      </SafeAreaView>
    );
  }

  // Phone/sheet mode: original layout using Screen component
  return (
    <Screen title="New room" subtitle="A shared space for a team, project, or moment">
      {error ? <Text style={styles.error}>{error}</Text> : null}
      <View style={styles.form}>
        <Text style={styles.label}>Room name</Text>
        <TextInput value={name} onChangeText={setName} placeholder="Monday standup" placeholderTextColor={colors.text3} style={styles.input} autoFocus />
        <Text style={styles.label}>Passcode · optional</Text>
        <TextInput value={passcode} onChangeText={setPasscode} placeholder="Leave blank for one-tap access" placeholderTextColor={colors.text3} style={styles.input} secureTextEntry />
        <View style={styles.switchRow}>
          <View style={styles.switchCopy}>
            <Text style={styles.switchTitle}>Guest access</Text>
            <Text style={styles.switchSubtitle}>Allow a revocable guest link for this room.</Text>
          </View>
          <Switch value={guestAccess} onValueChange={setGuestAccess} />
        </View>
        <Pressable
          accessibilityRole="button"
          onPress={create} disabled={!name.trim() || busy} style={({ pressed }) => [styles.create, pressed && styles.pressed, (!name.trim() || busy) && styles.disabled]}>
          <Text style={styles.createText}>{busy ? 'Creating…' : 'Create room'}</Text>
        </Pressable>
      </View>
    </Screen>
  );
}

const styles = StyleSheet.create({
  // Phone/sheet mode styles
  form: { gap: space[3] },
  label: { ...type.label, color: colors.text3, textTransform: 'uppercase', marginTop: space[2] },
  input: { minHeight: hitMin, borderRadius: radius.md, backgroundColor: colors.surface1, paddingHorizontal: space[4], fontSize: 15, color: colors.text1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  switchRow: { minHeight: 74, flexDirection: 'row', alignItems: 'center', backgroundColor: colors.surface1, borderRadius: radius.lg, paddingHorizontal: space[4], marginTop: space[2] },
  switchCopy: { flex: 1, paddingRight: space[3] },
  switchTitle: { ...type.bodyMedium, color: colors.text1 },
  switchSubtitle: { ...type.caption, color: colors.text2, marginTop: 2 },
  create: { minHeight: 50, borderRadius: radius.lg, backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center', marginTop: space[3] },
  createText: { ...type.button, color: colors.onAccent },
  pressed: { opacity: 0.82, transform: [{ scale: 0.98 }] },
  disabled: { opacity: 0.45 },
  error: { ...type.bodySm, color: colors.danger, backgroundColor: colors.dangerSoft, padding: space[3], borderRadius: radius.md, marginBottom: space[3] },

  // Workstation mode styles (iPad ≥1024, rail visible, web-class layout)
  workstationSafe: { flex: 1, backgroundColor: colors.bgApp },
  workstationHeader: {
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingHorizontal: space[5],
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line1,
  },
  workstationBack: {
    width: 40,
    height: 40,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.md,
  },
  workstationHeaderText: { flex: 1, gap: 2 },
  workstationTitle: { ...type.title2, color: colors.text1 },
  workstationSubtitle: { ...type.caption, color: colors.text2 },
  workstationScroll: { flexGrow: 1, paddingVertical: space[6] },
  workstationContent: {
    maxWidth: 480,
    alignSelf: 'center',
    width: '100%',
    paddingHorizontal: space[5],
    gap: space[5],
  },
  workstationError: {
    ...type.bodySm,
    color: colors.danger,
    backgroundColor: colors.dangerSoft,
    padding: space[4],
    borderRadius: radius.lg,
  },
  workstationCard: {
    gap: space[4],
    padding: space[5],
    borderRadius: radius.xl,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
  },
  workstationFieldGroup: { gap: space[2] },
  workstationLabel: { ...type.label, color: colors.text2, textTransform: 'uppercase' },
  workstationInput: {
    minHeight: 52,
    borderRadius: radius.lg,
    backgroundColor: colors.surface2,
    paddingHorizontal: space[4],
    fontSize: 15,
    color: colors.text1,
  },
  workstationSwitchRow: {
    minHeight: 64,
    flexDirection: 'row',
    alignItems: 'center',
    paddingTop: space[3],
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: colors.line1,
  },
  workstationSwitchCopy: { flex: 1, paddingRight: space[3] },
  workstationSwitchTitle: { ...type.bodyMedium, color: colors.text1 },
  workstationSwitchSubtitle: { ...type.caption, color: colors.text2, marginTop: 2 },
  workstationCreate: {
    minHeight: 52,
    borderRadius: radius.lg,
    backgroundColor: colors.accent,
    alignItems: 'center',
    justifyContent: 'center',
  },
  workstationCreateText: { ...type.button, color: colors.onAccent },
  workstationDisabled: { opacity: 0.45 },
});
