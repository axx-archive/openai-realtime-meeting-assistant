import React, { useCallback, useEffect, useState } from 'react';
import {
  Alert,
  Image,
  Pressable,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from 'react-native';
import * as Haptics from 'expo-haptics';
import * as ImagePicker from 'expo-image-picker';
import * as Passkeys from 'react-native-passkeys';
import { SymbolView } from 'expo-symbols';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { passkeyErrorMessage } from '../auth/passkeyError';
import { Screen } from '../components/Screen';
import { useShowPreviews } from '../canvas/previewPreference';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';

type PasskeyRow = { id: string; label: string };
const themes = ['system', 'light', 'dark'] as const;

export function SettingsScreen() {
  const {
    user,
    sessionToken,
    updateIdentity,
    changePassword,
    signOut,
  } = useAuth();
  const { showPreviews, setShowPreviews } = useShowPreviews();
  const [displayName, setDisplayName] = useState(user?.name ?? '');
  const [passkeys, setPasskeys] = useState<PasskeyRow[]>([]);
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadPasskeys = useCallback(async () => {
    if (!sessionToken) return;
    try {
      const response = await api.passkeys(sessionToken);
      setPasskeys(response.passkeys ?? []);
    } catch {
      setPasskeys([]);
    }
  }, [sessionToken]);

  useEffect(() => {
    void loadPasskeys();
  }, [loadPasskeys]);

  function fail(err: unknown) {
    setStatus(null);
    setError(err instanceof BonfireApiError || err instanceof Error ? err.message : 'Something went wrong.');
  }

  async function saveProfile(avatarDataURL = user?.avatarDataURL ?? '') {
    if (!sessionToken || !displayName.trim()) return;
    setBusy('profile');
    setError(null);
    try {
      const identity = await api.updateProfile(sessionToken, displayName.trim(), avatarDataURL);
      updateIdentity(identity);
      setDisplayName(identity.name);
      setStatus('Profile updated.');
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      fail(err);
    } finally {
      setBusy(null);
    }
  }

  async function chooseAvatar() {
    const result = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ['images'],
      allowsEditing: true,
      aspect: [1, 1],
      quality: 0.72,
      base64: true,
    });
    if (result.canceled || !result.assets[0]?.base64) return;
    const asset = result.assets[0];
    await saveProfile(`data:${asset.mimeType ?? 'image/jpeg'};base64,${asset.base64}`);
  }

  async function setTheme(theme: (typeof themes)[number]) {
    if (!sessionToken) return;
    setBusy('theme');
    setError(null);
    try {
      const identity = await api.setTheme(sessionToken, theme);
      updateIdentity(identity);
      setStatus(`Appearance set to ${theme}.`);
      await Haptics.selectionAsync();
    } catch (err) {
      fail(err);
    } finally {
      setBusy(null);
    }
  }

  async function addPasskey() {
    if (!sessionToken || !Passkeys.isSupported()) {
      setError('Passkeys are not available on this device.');
      return;
    }
    setBusy('passkey');
    setError(null);
    try {
      const begin = await api.beginPasskeyRegistration(sessionToken);
      const credential = await Passkeys.create(begin.publicKey as never);
      if (!credential) return;
      const response = await api.finishPasskeyRegistration(
        sessionToken,
        begin.ceremony,
        credential,
      );
      setPasskeys(response.passkeys ?? []);
      setStatus('Passkey added. It now works here and on the web.');
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      const message = passkeyErrorMessage(err);
      if (message) {
        setStatus(null);
        setError(message);
      }
    } finally {
      setBusy(null);
    }
  }

  function confirmDeletePasskey(row: PasskeyRow) {
    Alert.alert('Remove this passkey?', 'You will no longer be able to use it to sign in.', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Remove',
        style: 'destructive',
        onPress: async () => {
          if (!sessionToken) return;
          try {
            const response = await api.deletePasskey(sessionToken, row.id);
            setPasskeys(response.passkeys ?? []);
            await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
          } catch (err) {
            fail(err);
          }
        },
      },
    ]);
  }

  async function savePassword() {
    if (newPassword.length < 8) {
      setError('The new password needs at least 8 characters.');
      return;
    }
    setBusy('password');
    setError(null);
    try {
      await changePassword(currentPassword, newPassword);
      setCurrentPassword('');
      setNewPassword('');
      setStatus('Password changed. Other devices were signed out.');
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      fail(err);
    } finally {
      setBusy(null);
    }
  }

  return (
    <Screen title="Settings" subtitle={user?.email}>
      {error ? <Text style={styles.error}>{error}</Text> : null}
      {status ? <Text style={styles.status}>{status}</Text> : null}

      <Text style={styles.sectionTitle}>Profile</Text>
      <View style={[styles.section, shadow[1]]}>
        <Pressable onPress={chooseAvatar} style={styles.avatarButton}>
          {user?.avatarDataURL ? (
            <Image source={{ uri: user.avatarDataURL }} style={styles.avatar} />
          ) : (
            <View style={[styles.avatar, styles.avatarFallback]}>
              <Text style={styles.avatarInitial}>{(user?.name || '?').slice(0, 1).toUpperCase()}</Text>
            </View>
          )}
          <Text style={styles.avatarAction}>Change photo</Text>
        </Pressable>
        <TextInput
          accessibilityLabel="Display name"
          value={displayName}
          onChangeText={setDisplayName}
          style={styles.input}
          placeholder="Display name"
          placeholderTextColor={colors.text3}
        />
        <Pressable
          onPress={() => void saveProfile()}
          disabled={busy === 'profile'}
          style={({ pressed }) => [styles.primary, pressed && styles.pressed]}
        >
          <Text style={styles.primaryText}>{busy === 'profile' ? 'Saving…' : 'Save profile'}</Text>
        </Pressable>
      </View>

      <Text style={styles.sectionTitle}>Appearance</Text>
      <View style={[styles.segment, shadow[1]]}>
        {themes.map((theme) => {
          const selected = (user?.themePref ?? 'system') === theme;
          return (
            <Pressable
              key={theme}
              onPress={() => void setTheme(theme)}
              style={[styles.segmentItem, selected && styles.segmentSelected]}
            >
              <Text style={[styles.segmentText, selected && styles.segmentTextSelected]}>
                {theme[0].toUpperCase() + theme.slice(1)}
              </Text>
            </Pressable>
          );
        })}
      </View>

      <Text style={styles.sectionTitle}>Privacy</Text>
      <View style={[styles.section, shadow[1]]}>
        <Pressable
          accessibilityRole="switch"
          accessibilityState={{ checked: showPreviews }}
          accessibilityLabel="Show message previews"
          accessibilityHint="When off, the home screen shows how many messages are waiting instead of what they say."
          onPress={() => setShowPreviews(!showPreviews)}
          style={({ pressed }) => [styles.toggleRow, pressed && styles.pressed]}
        >
          <View style={styles.toggleText}>
            <Text style={styles.toggleLabel}>Show message previews</Text>
            <Text style={styles.toggleHint}>
              Your team&rsquo;s latest message appears on the home screen. Turn this off to show a
              count instead.
            </Text>
          </View>
          <Switch value={showPreviews} onValueChange={setShowPreviews} />
        </Pressable>
      </View>

      <Text style={styles.sectionTitle}>Passkeys</Text>
      <View style={[styles.section, shadow[1]]}>
        {passkeys.map((row) => (
          <View key={row.id} style={styles.passkeyRow}>
            <SymbolView name="key.fill" tintColor={colors.text2} size={17} />
            <Text style={styles.passkeyLabel}>{row.label}</Text>
            <Pressable onPress={() => confirmDeletePasskey(row)} hitSlop={12}>
              <Text style={styles.remove}>Remove</Text>
            </Pressable>
          </View>
        ))}
        <Pressable onPress={addPasskey} style={({ pressed }) => [styles.secondary, pressed && styles.pressed]}>
          <SymbolView name="plus.circle.fill" tintColor={colors.text1} size={18} />
          <Text style={styles.secondaryText}>{busy === 'passkey' ? 'Waiting…' : 'Add a passkey'}</Text>
        </Pressable>
      </View>

      <Text style={styles.sectionTitle}>Password</Text>
      <View style={[styles.section, shadow[1]]}>
        <TextInput
          secureTextEntry
          autoComplete="current-password"
          placeholder="Current password"
          placeholderTextColor={colors.text3}
          value={currentPassword}
          onChangeText={setCurrentPassword}
          style={styles.input}
        />
        <TextInput
          secureTextEntry
          autoComplete="new-password"
          placeholder="New password"
          placeholderTextColor={colors.text3}
          value={newPassword}
          onChangeText={setNewPassword}
          style={styles.input}
        />
        <Pressable onPress={savePassword} style={({ pressed }) => [styles.secondary, pressed && styles.pressed]}>
          <Text style={styles.secondaryText}>{busy === 'password' ? 'Updating…' : 'Change password'}</Text>
        </Pressable>
      </View>

      <Pressable onPress={() => void signOut()} style={({ pressed }) => [styles.signOut, pressed && styles.pressed]}>
        <Text style={styles.signOutText}>Sign out</Text>
      </Pressable>
    </Screen>
  );
}

const styles = StyleSheet.create({
  sectionTitle: { ...type.label, color: colors.text3, textTransform: 'uppercase', marginTop: space[4], marginBottom: space[2] },
  section: { backgroundColor: colors.surface1, borderRadius: radius.xl, padding: space[4], gap: space[3], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  error: { ...type.bodySm, color: colors.danger, backgroundColor: colors.dangerSoft, padding: space[3], borderRadius: radius.md },
  status: { ...type.bodySm, color: colors.live, backgroundColor: colors.liveSoft, padding: space[3], borderRadius: radius.md },
  avatarButton: { flexDirection: 'row', alignItems: 'center', gap: space[3] },
  avatar: { width: 58, height: 58, borderRadius: 20 },
  avatarFallback: { backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center' },
  avatarInitial: { ...type.title2, color: colors.onAccent },
  avatarAction: { ...type.bodyMedium, color: colors.text1 },
  input: { minHeight: hitMin, borderRadius: radius.md, paddingHorizontal: space[3], backgroundColor: colors.surface3, color: colors.text1, fontSize: 15 },
  primary: { minHeight: hitMin, borderRadius: radius.md, backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center' },
  primaryText: { ...type.button, color: colors.onAccent },
  secondary: { minHeight: hitMin, borderRadius: radius.md, backgroundColor: colors.surface3, alignItems: 'center', justifyContent: 'center', flexDirection: 'row', gap: 8 },
  secondaryText: { ...type.button, color: colors.text1 },
  pressed: { transform: [{ scale: 0.96 }], opacity: 0.88 },
  segment: { flexDirection: 'row', backgroundColor: colors.surface1, borderRadius: radius.lg, padding: 4, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  segmentItem: { flex: 1, minHeight: 38, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md },
  segmentSelected: { backgroundColor: colors.accent },
  segmentText: { ...type.button, color: colors.text2 },
  segmentTextSelected: { color: colors.onAccent },
  toggleRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingVertical: space[2],
  },
  toggleText: { flex: 1, gap: 2 },
  toggleLabel: {
    ...type.bodyMedium,
    color: colors.text1,
  },
  toggleHint: {
    ...type.caption,
    color: colors.text2,
  },
  passkeyRow: { minHeight: 42, flexDirection: 'row', alignItems: 'center', gap: 10 },
  passkeyLabel: { ...type.bodySm, color: colors.text1, flex: 1 },
  remove: { ...type.captionMedium, color: colors.danger },
  signOut: { minHeight: hitMin, marginTop: space[6], alignItems: 'center', justifyContent: 'center' },
  signOutText: { ...type.button, color: colors.danger },
});
