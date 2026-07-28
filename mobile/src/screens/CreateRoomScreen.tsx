import React, { useState } from 'react';
import { Pressable, StyleSheet, Switch, Text, TextInput, View } from 'react-native';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import * as Haptics from 'expo-haptics';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Screen } from '../components/Screen';
import type { RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'CreateRoom'>;

export function CreateRoomScreen({ navigation }: Props) {
  const { sessionToken } = useAuth();
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
  form: { gap: space[3] },
  label: { ...type.label, color: colors.text3, textTransform: 'uppercase', marginTop: space[2] },
  input: { minHeight: hitMin, borderRadius: radius.md, backgroundColor: colors.surface1, paddingHorizontal: space[4], fontSize: 15, color: colors.text1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  switchRow: { minHeight: 74, flexDirection: 'row', alignItems: 'center', backgroundColor: colors.surface1, borderRadius: radius.lg, paddingHorizontal: space[4], marginTop: space[2] },
  switchCopy: { flex: 1, paddingRight: space[3] },
  switchTitle: { ...type.bodyMedium, color: colors.text1 },
  switchSubtitle: { ...type.caption, color: colors.text2, marginTop: 2 },
  create: { minHeight: 50, borderRadius: radius.lg, backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center', marginTop: space[3] },
  createText: { ...type.button, color: colors.onAccent },
  pressed: { transform: [{ scale: 0.96 }] },
  disabled: { opacity: 0.45 },
  error: { ...type.bodySm, color: colors.danger, backgroundColor: colors.dangerSoft, padding: space[3], borderRadius: radius.md, marginBottom: space[3] },
});
