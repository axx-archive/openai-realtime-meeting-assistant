import React, { useEffect, useState } from 'react';
import {
  ActionSheetIOS,
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
  useColorScheme,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { LinearGradient } from 'expo-linear-gradient';
import * as Linking from 'expo-linking';
import * as Haptics from 'expo-haptics';
import { SymbolView } from 'expo-symbols';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { passkeyErrorMessage } from '../auth/passkeyError';
import { BrandMark } from '../components/BrandMark';
import { API_BASE_URL } from '../config';
import { colors, hitMin, product, radius, shadow, space, type } from '../theme/tokens';

const teamAccounts = ['Joel', 'Caitlyn', 'Tyler', 'AJ', 'Tim', 'Erick', 'Tom'] as const;

/**
 * Login gate — matches live `.login-head` / `.login-card` / `.login-signin`
 * from index.html (Glass & Ink), with a native account sheet.
 */
export function LoginScreen() {
  const { signIn, signInWithPasskey, lastLoginName } = useAuth();
  const [name, setName] = useState(() =>
    teamAccounts.find((account) => account.toLocaleLowerCase() === lastLoginName.trim().toLocaleLowerCase()) ?? '',
  );
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [passkeyBusy, setPasskeyBusy] = useState(false);
  const [recoveryMode, setRecoveryMode] = useState(false);
  const [recoveryEmail, setRecoveryEmail] = useState('');
  const [resetToken, setResetToken] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [notice, setNotice] = useState<string | null>(null);
  const incomingURL = Linking.useURL();
  const dark = useColorScheme() === 'dark';

  useEffect(() => {
    if (!incomingURL) return;
    const parsed = Linking.parse(incomingURL);
    const token = parsed.queryParams?.reset;
    if (typeof token === 'string' && token.trim()) {
      setResetToken(token.trim());
      setRecoveryMode(false);
      setNotice(null);
    }
  }, [incomingURL]);

  async function onSubmit() {
    if (!name.trim() || !password) {
      setError('Select your account and enter your password.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await signIn(name, password);
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      const message =
        err instanceof BonfireApiError
          ? err.message
          : err instanceof Error
            ? err.message
            : 'Sign-in failed.';
      setError(message);
    } finally {
      setBusy(false);
    }
  }

  function selectAccount() {
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title: 'Select your account',
        message: 'Choose your name, then enter your password.',
        options: ['Cancel', ...teamAccounts],
        cancelButtonIndex: 0,
      },
      (index) => {
        const account = teamAccounts[index - 1];
        if (!account) return;
        setName(account);
        setError(null);
        void Haptics.selectionAsync();
      },
    );
  }

  async function onPasskey() {
    setPasskeyBusy(true);
    setError(null);
    try {
      await signInWithPasskey();
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      const message = passkeyErrorMessage(err);
      if (message) setError(message);
    } finally {
      setPasskeyBusy(false);
    }
  }

  async function requestReset() {
    if (!recoveryEmail.trim()) {
      setError('Enter your account email.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.requestPasswordReset(recoveryEmail);
      setNotice('If that address has an account, a reset link is on its way.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not request a reset link.');
    } finally {
      setBusy(false);
    }
  }

  async function finishReset() {
    if (newPassword.length < 8) {
      setError('Use at least 8 characters.');
      return;
    }
    if (newPassword !== confirmPassword) {
      setError('Passwords do not match.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.confirmPasswordReset(resetToken, newPassword);
      setResetToken('');
      setNewPassword('');
      setConfirmPassword('');
      setNotice('Password changed. Sign in with your new password.');
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not reset your password.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <LinearGradient
      colors={dark ? ['#09090B', '#101013', '#1B1B21'] : ['#FFFFFF', '#F5F5F7', '#ECECF1']}
      style={styles.flex}
    >
    <SafeAreaView style={styles.safe}>
      <KeyboardAvoidingView
        style={styles.flex}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      >
        <View style={styles.inner}>
          <View style={styles.head}>
            <BrandMark size={56} />
            <Text style={styles.wordmark}>{product.wordmark}</Text>
          </View>

          <View style={[styles.card, shadow.glass]}>
            {resetToken ? (
              <>
                <Text style={styles.recoveryTitle}>Choose a new password</Text>
                <Text style={styles.recoveryBody}>This reset link opened securely in BonfireOS.</Text>
                <TextInput
                  accessibilityLabel="New password"
                  secureTextEntry
                  autoCapitalize="none"
                  autoComplete="new-password"
                  placeholder="new password"
                  placeholderTextColor={colors.text3}
                  style={styles.input}
                  value={newPassword}
                  onChangeText={setNewPassword}
                />
                <TextInput
                  accessibilityLabel="Confirm new password"
                  secureTextEntry
                  autoCapitalize="none"
                  autoComplete="new-password"
                  placeholder="confirm password"
                  placeholderTextColor={colors.text3}
                  style={styles.input}
                  value={confirmPassword}
                  onChangeText={setConfirmPassword}
                  onSubmitEditing={finishReset}
                />
                {error ? <Text style={styles.error}>{error}</Text> : null}
                <Pressable onPress={finishReset} disabled={busy} style={({ pressed }) => [styles.cta, (pressed || busy) && styles.ctaPressed]}>
                  {busy ? <ActivityIndicator color={colors.onAccent} /> : <Text style={styles.ctaText}>Reset password</Text>}
                </Pressable>
                <Pressable
                  onPress={() => {
                    setResetToken('');
                    setNewPassword('');
                    setConfirmPassword('');
                    setError(null);
                  }}
                  style={styles.quietAction}
                >
                  <Text style={styles.quietActionText}>Back to sign in</Text>
                </Pressable>
              </>
            ) : recoveryMode ? (
              <>
                <Text style={styles.recoveryTitle}>Reset your password</Text>
                <Text style={styles.recoveryBody}>We’ll email a secure link if this address belongs to an account.</Text>
                <TextInput
                  accessibilityLabel="Account email"
                  autoCapitalize="none"
                  autoCorrect={false}
                  autoComplete="email"
                  keyboardType="email-address"
                  placeholder="you@company.com"
                  placeholderTextColor={colors.text3}
                  style={styles.input}
                  value={recoveryEmail}
                  onChangeText={setRecoveryEmail}
                  onSubmitEditing={requestReset}
                />
                {error ? <Text style={styles.error}>{error}</Text> : null}
                {notice ? <Text style={styles.notice}>{notice}</Text> : null}
                <Pressable onPress={requestReset} disabled={busy} style={({ pressed }) => [styles.cta, (pressed || busy) && styles.ctaPressed]}>
                  {busy ? <ActivityIndicator color={colors.onAccent} /> : <Text style={styles.ctaText}>Send reset link</Text>}
                </Pressable>
                <Pressable onPress={() => { setRecoveryMode(false); setError(null); setNotice(null); }} style={styles.quietAction}>
                  <Text style={styles.quietActionText}>Back to sign in</Text>
                </Pressable>
              </>
            ) : (
              <>
              <Pressable
                accessibilityLabel={name ? `Account, ${name}` : 'Select your account'}
                accessibilityHint="Opens the team account picker."
                accessibilityRole="button"
                disabled={busy || passkeyBusy}
                onPress={selectAccount}
                style={({ pressed }) => [styles.accountPicker, pressed && styles.accountPickerPressed]}
              >
                <View style={styles.accountPickerCopy}>
                  <Text style={styles.accountPickerLabel}>ACCOUNT</Text>
                  <Text style={[styles.accountPickerValue, !name && styles.accountPickerPlaceholder]}>
                    {name || 'Select your account'}
                  </Text>
                </View>
                <SymbolView name="chevron.up.chevron.down" tintColor={colors.text2} size={15} />
              </Pressable>

            <TextInput
              accessibilityLabel="Password"
              secureTextEntry
              autoCapitalize="none"
              autoComplete="password"
              placeholder="password"
              placeholderTextColor={colors.text3}
              style={styles.input}
              value={password}
              onChangeText={setPassword}
              editable={!busy}
              returnKeyType="go"
              onSubmitEditing={onSubmit}
            />

            <Pressable
              accessibilityRole="button"
              onPress={() => { setRecoveryEmail(''); setRecoveryMode(true); setError(null); setNotice(null); }}
              style={styles.forgot}
            >
              <Text style={styles.forgotText}>Forgot password?</Text>
            </Pressable>

            {error ? <Text style={styles.error}>{error}</Text> : null}
            {notice ? <Text style={styles.notice}>{notice}</Text> : null}

            <Pressable
              accessibilityRole="button"
              accessibilityLabel={product.loginCta}
              accessibilityHint="Signs in with your selected account and password."
              accessibilityState={{ disabled: busy || passkeyBusy, busy }}
              onPress={onSubmit}
              disabled={busy || passkeyBusy}
              style={({ pressed }) => [
                styles.cta,
                (pressed || busy || passkeyBusy) && styles.ctaPressed,
                (busy || passkeyBusy) && styles.ctaDisabled,
              ]}
            >
              {busy ? (
                <ActivityIndicator color={colors.onAccent} />
              ) : (
                <Text style={styles.ctaText}>{product.loginCta}</Text>
              )}
            </Pressable>

            <View style={styles.dividerRow}>
              <View style={styles.divider} />
              <Text style={styles.dividerText}>or</Text>
              <View style={styles.divider} />
            </View>

            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Sign in with a passkey"
              accessibilityHint="Uses a passkey saved to this device or your iCloud Keychain."
              accessibilityState={{ disabled: busy || passkeyBusy, busy: passkeyBusy }}
              onPress={onPasskey}
              disabled={busy || passkeyBusy}
              style={({ pressed }) => [styles.passkey, pressed && styles.passkeyPressed]}
            >
              {passkeyBusy ? (
                <ActivityIndicator color={colors.text1} />
              ) : (
                <>
                  <SymbolView name="person.badge.key.fill" tintColor={colors.text1} size={19} />
                  <Text style={styles.passkeyText}>Sign in with a passkey</Text>
                </>
              )}
            </Pressable>
              </>
            )}
          </View>

          <Text style={styles.footer}>
            {API_BASE_URL.replace(/^https?:\/\//, '')}
          </Text>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
    </LinearGradient>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  flex: {
    flex: 1,
  },
  inner: {
    flex: 1,
    paddingHorizontal: space[5],
    justifyContent: 'center',
    maxWidth: 440,
    width: '100%',
    alignSelf: 'center',
  },
  head: {
    alignItems: 'center',
    gap: 14,
    marginBottom: space[6],
  },
  wordmark: {
    ...type.wordmark,
    color: colors.text1,
  },
  card: {
    alignSelf: 'stretch',
    backgroundColor: colors.glassPanel,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.glassBorder,
    borderRadius: radius.xxl,
    padding: space[6],
    gap: 10,
  },
  input: {
    height: hitMin,
    borderRadius: radius.md,
    paddingHorizontal: space[4],
    backgroundColor: colors.surface3,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    color: colors.text1,
    fontSize: 14,
    letterSpacing: -0.07,
  },
  accountPicker: {
    minHeight: 58,
    paddingHorizontal: space[4],
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: space[3],
    borderRadius: radius.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface3,
  },
  accountPickerPressed: { opacity: 0.9, transform: [{ scale: 0.99 }] },
  accountPickerCopy: { flex: 1, minWidth: 0, gap: 2 },
  accountPickerLabel: { ...type.label, color: colors.text3, fontSize: 9, letterSpacing: 0.9 },
  accountPickerValue: { ...type.body, color: colors.text1 },
  accountPickerPlaceholder: { color: colors.text2 },
  error: {
    ...type.caption,
    color: colors.danger,
  },
  notice: {
    ...type.caption,
    color: colors.text2,
  },
  recoveryTitle: {
    ...type.title2,
    color: colors.text1,
  },
  recoveryBody: {
    ...type.bodySm,
    color: colors.text2,
    marginBottom: space[2],
  },
  forgot: {
    alignSelf: 'flex-end',
    minHeight: 34,
    justifyContent: 'center',
  },
  forgotText: {
    ...type.caption,
    color: colors.text2,
    textDecorationLine: 'underline',
  },
  quietAction: {
    minHeight: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
  },
  quietActionText: {
    ...type.button,
    color: colors.text2,
  },
  cta: {
    marginTop: 2,
    height: hitMin,
    borderRadius: radius.md,
    backgroundColor: colors.accent,
    alignItems: 'center',
    justifyContent: 'center',
  },
  ctaPressed: {
    transform: [{ scale: 0.96 }],
    opacity: 0.94,
  },
  ctaDisabled: {
    opacity: 0.7,
  },
  ctaText: {
    ...type.button,
    color: colors.onAccent,
  },
  dividerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
    marginVertical: 4,
  },
  divider: {
    flex: 1,
    height: StyleSheet.hairlineWidth,
    backgroundColor: colors.line2,
  },
  dividerText: {
    ...type.caption,
    color: colors.text3,
  },
  passkey: {
    height: hitMin,
    borderRadius: radius.md,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    flexDirection: 'row',
    gap: 8,
    alignItems: 'center',
    justifyContent: 'center',
  },
  passkeyPressed: {
    transform: [{ scale: 0.96 }],
    backgroundColor: colors.surface3,
  },
  passkeyText: {
    ...type.button,
    color: colors.text1,
  },
  footer: {
    ...type.label,
    color: colors.text3,
    textAlign: 'center',
    marginTop: space[6],
    textTransform: 'none',
  },
});
