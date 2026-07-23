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
import { BrandMark } from '../components/BrandMark';
import { API_BASE_URL } from '../config';
import { colors, hitMin, product, radius, shadow, space, type } from '../theme/tokens';

/**
 * Login gate — matches live `.login-head` / `.login-card` / `.login-signin`
 * from index.html (Glass & Ink). Placeholder-only fields; ink primary CTA.
 */
export function LoginScreen() {
  const { signIn, lastLoginName } = useAuth();
  const [name, setName] = useState(lastLoginName);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit() {
    if (!name.trim() || !password) {
      setError('choose your name and enter your password');
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
            : 'sign-in failed';
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
          <View style={styles.head}>
            <BrandMark size={56} />
            <Text style={styles.wordmark}>{product.wordmark}</Text>
          </View>

          <View style={[styles.card, shadow.glass]}>
            <TextInput
              accessibilityLabel="Name"
              autoCapitalize="words"
              autoCorrect={false}
              autoComplete="username"
              placeholder="choose your name"
              placeholderTextColor={colors.text3}
              style={styles.input}
              value={name}
              onChangeText={setName}
              editable={!busy}
              returnKeyType="next"
            />

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

            {error ? <Text style={styles.error}>{error}</Text> : null}

            <Pressable
              onPress={onSubmit}
              disabled={busy}
              style={({ pressed }) => [
                styles.cta,
                (pressed || busy) && styles.ctaPressed,
                busy && styles.ctaDisabled,
              ]}
            >
              {busy ? (
                <ActivityIndicator color={colors.onAccent} />
              ) : (
                <Text style={styles.ctaText}>{product.loginCta}</Text>
              )}
            </Pressable>
          </View>

          <Text style={styles.footer}>
            {API_BASE_URL.replace(/^https?:\/\//, '')}
          </Text>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
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
  error: {
    ...type.caption,
    color: colors.danger,
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
    transform: [{ scale: 0.97 }],
    opacity: 0.94,
  },
  ctaDisabled: {
    opacity: 0.7,
  },
  ctaText: {
    ...type.button,
    color: colors.onAccent,
  },
  footer: {
    ...type.label,
    color: colors.text3,
    textAlign: 'center',
    marginTop: space[6],
    textTransform: 'none',
  },
});
