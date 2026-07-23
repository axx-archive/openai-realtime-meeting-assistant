import React, { useState } from 'react';
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
import { SafeAreaView } from 'react-native-safe-area-context';
import { BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL } from '../config';
import { colors } from '../theme/colors';

export function LoginScreen() {
  const { signIn, lastLoginName } = useAuth();
  const [name, setName] = useState(lastLoginName);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit() {
    if (!name.trim() || !password) {
      setError('Enter your roster name and password.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await signIn(name, password);
    } catch (err) {
      const message =
        err instanceof BonfireApiError
          ? err.message
          : err instanceof Error
            ? err.message
            : 'Sign-in failed';
      setError(message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <SafeAreaView style={styles.safe}>
      <KeyboardAvoidingView
        style={styles.flex}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      >
        <View style={styles.inner}>
          <View style={styles.brandMark}>
            <View style={styles.ember} />
          </View>
          <Text style={styles.brand}>BonfireOS</Text>
          <Text style={styles.tagline}>
            The company OS on your phone — same accounts, rooms, Scout, and board as
            the web.
          </Text>

          <View style={styles.form}>
            <Text style={styles.label}>Name</Text>
            <TextInput
              autoCapitalize="words"
              autoCorrect={false}
              autoComplete="username"
              placeholder="Your roster name"
              placeholderTextColor={colors.textTertiary}
              style={styles.input}
              value={name}
              onChangeText={setName}
              editable={!busy}
              returnKeyType="next"
            />

            <Text style={styles.label}>Password</Text>
            <TextInput
              secureTextEntry
              autoCapitalize="none"
              autoComplete="password"
              placeholder="Password"
              placeholderTextColor={colors.textTertiary}
              style={styles.input}
              value={password}
              onChangeText={setPassword}
              editable={!busy}
              returnKeyType="go"
              onSubmitEditing={onSubmit}
            />

            {error ? <Text style={styles.error}>{error}</Text> : null}

            <Pressable
              onPress={onSubmit}
              disabled={busy}
              style={({ pressed }) => [
                styles.button,
                (pressed || busy) && styles.buttonPressed,
              ]}
            >
              {busy ? (
                <ActivityIndicator color={colors.onAccent} />
              ) : (
                <Text style={styles.buttonText}>Sign in</Text>
              )}
            </Pressable>
          </View>

          <Text style={styles.footer}>
            Signing into {API_BASE_URL.replace(/^https?:\/\//, '')}
          </Text>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  flex: {
    flex: 1,
  },
  inner: {
    flex: 1,
    paddingHorizontal: 24,
    paddingTop: 48,
    justifyContent: 'center',
  },
  brandMark: {
    width: 48,
    height: 48,
    borderRadius: 14,
    backgroundColor: colors.accent,
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: 18,
  },
  ember: {
    width: 18,
    height: 18,
    borderRadius: 9,
    backgroundColor: colors.ember,
  },
  brand: {
    fontSize: 34,
    fontWeight: '700',
    letterSpacing: -1,
    color: colors.text,
  },
  tagline: {
    marginTop: 10,
    fontSize: 16,
    lineHeight: 23,
    color: colors.textSecondary,
    maxWidth: 340,
  },
  form: {
    marginTop: 36,
    gap: 8,
  },
  label: {
    marginTop: 10,
    fontSize: 13,
    fontWeight: '600',
    color: colors.textSecondary,
    textTransform: 'uppercase',
    letterSpacing: 0.5,
  },
  input: {
    backgroundColor: colors.bgElevated,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
    borderRadius: 14,
    paddingHorizontal: 16,
    paddingVertical: 14,
    fontSize: 17,
    color: colors.text,
  },
  error: {
    marginTop: 8,
    color: colors.danger,
    fontSize: 14,
    lineHeight: 20,
  },
  button: {
    marginTop: 18,
    backgroundColor: colors.accent,
    borderRadius: 14,
    paddingVertical: 16,
    alignItems: 'center',
  },
  buttonPressed: {
    opacity: 0.88,
  },
  buttonText: {
    color: colors.onAccent,
    fontSize: 17,
    fontWeight: '600',
  },
  footer: {
    marginTop: 28,
    fontSize: 12,
    color: colors.textTertiary,
    textAlign: 'center',
  },
});
