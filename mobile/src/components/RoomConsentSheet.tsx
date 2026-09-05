import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';
import { api, BonfireApiError } from '../api/client';
import type {
  ConsentDisposition,
  ConsentScope,
  ConsentStatus,
} from '../api/types';
import { hitMin, radius, shadow, space, type } from '../theme/tokens';
import { callColors } from '../theme/callTokens';

export type RoomConsentSheetProps = {
  sessionToken: string;
  visible: boolean;
  onClose: () => void;
};

type ConsentChoice = {
  scope: ConsentScope;
  title: string;
  detail: string;
  dependency: string;
  icon: SFSymbol;
};

const choices: ConsentChoice[] = [
  {
    scope: 'audio_capture',
    title: 'Server microphone copy',
    detail: 'Lets Bonfire receive a server-side copy of your microphone for this room sitting.',
    dependency: 'Your direct call audio never depends on this.',
    icon: 'waveform',
  },
  {
    scope: 'transcription',
    title: 'Transcript',
    detail: 'Turns an allowed microphone copy into text for the meeting record.',
    dependency: 'Requires Server microphone copy.',
    icon: 'text.quote',
  },
  {
    scope: 'model_analysis',
    title: 'Scout analysis',
    detail: 'Lets Scout analyze your allowed transcript while the meeting is happening.',
    dependency: 'Requires Transcript.',
    icon: 'sparkles',
  },
  {
    scope: 'org_memory',
    title: 'Company memory',
    detail: 'Lets approved meeting content contribute to durable company memory.',
    dependency: 'Requires Scout analysis.',
    icon: 'brain.head.profile',
  },
];

function errorCopy(error: unknown, action: 'load' | 'save'): string {
  if (error instanceof BonfireApiError) {
    if (error.status === 401) return 'Your session expired. Sign in again before changing microphone data choices.';
    if (error.status === 403) return 'Join the room before changing choices for this sitting.';
    if (error.status === 503) return 'Choices are unavailable right now, so server capture and derived uses stay off.';
  }
  return action === 'save'
    ? 'That choice could not be confirmed. Server capture and derived uses remain fail-closed.'
    : 'Could not load your choices. Server capture and derived uses remain fail-closed.';
}

function dispositionLabel(disposition: ConsentDisposition | undefined): string {
  switch (disposition) {
    case 'granted': return 'Allowed';
    case 'denied': return 'Off';
    case 'withdrawn': return 'Withdrawn';
    default: return 'Not chosen';
  }
}

export const RoomConsentSheet = memo(function RoomConsentSheet({
  sessionToken,
  visible,
  onClose,
}: RoomConsentSheetProps) {
  const [status, setStatus] = useState<ConsentStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<{
    scope: ConsentScope;
    disposition: ConsentDisposition;
  } | null>(null);
  const requestVersion = useRef(0);

  const loadStatus = useCallback(async () => {
    const version = ++requestVersion.current;
    setLoading(true);
    setError(null);
    if (!sessionToken.trim()) {
      setStatus(null);
      setLoading(false);
      setError('Sign in again before changing microphone data choices.');
      return;
    }
    try {
      const next = await api.getConsentStatus(sessionToken);
      if (requestVersion.current !== version) return;
      setStatus(next);
    } catch (caught) {
      if (requestVersion.current !== version) return;
      setStatus(null);
      setError(errorCopy(caught, 'load'));
    } finally {
      if (requestVersion.current === version) setLoading(false);
    }
  }, [sessionToken]);

  useEffect(() => {
    if (!visible) {
      requestVersion.current += 1;
      setPending(null);
      return undefined;
    }
    setStatus(null);
    void loadStatus();
    return () => {
      requestVersion.current += 1;
    };
  }, [loadStatus, visible]);

  const saveChoice = useCallback(async (
    scope: ConsentScope,
    disposition: ConsentDisposition,
  ) => {
    if (pending || !status?.storeAvailable || !status.choicesMutable) return;
    const version = ++requestVersion.current;
    setPending({ scope, disposition });
    setError(null);
    try {
      const result = await api.setConsentDecision(sessionToken, scope, disposition);
      if (requestVersion.current !== version) return;
      setStatus(result.consent);
    } catch (caught) {
      if (requestVersion.current !== version) return;
      setError(errorCopy(caught, 'save'));
    } finally {
      if (requestVersion.current === version) setPending(null);
    }
  }, [pending, sessionToken, status?.choicesMutable, status?.storeAvailable]);

  const close = useCallback(() => {
    if (!pending) onClose();
  }, [onClose, pending]);

  const policyLabel = useMemo(() => {
    if (!status?.policyVersion) return 'Choices for this sitting';
    return `This sitting · policy ${status.policyVersion}`;
  }, [status?.policyVersion]);

  return (
    <Modal
      animationType="slide"
      onRequestClose={close}
      presentationStyle="pageSheet"
      visible={visible}
    >
      <SafeAreaView style={styles.safe} edges={['top', 'right', 'bottom', 'left']}>
        <View accessibilityViewIsModal style={styles.sheet}>
          <View style={styles.header}>
            <View style={styles.headerIdentity}>
              <View style={styles.headerIcon}>
                <SymbolView name="lock.shield.fill" tintColor={callColors.text} size={19} />
              </View>
              <View style={styles.headerCopy}>
                <Text accessibilityRole="header" style={styles.title}>Microphone data</Text>
                <Text numberOfLines={1} style={styles.subtitle}>{policyLabel}</Text>
              </View>
            </View>
            <Pressable
              accessibilityLabel="Close microphone data choices"
              accessibilityRole="button"
              accessibilityState={{ disabled: Boolean(pending) }}
              disabled={Boolean(pending)}
              onPress={close}
              style={({ pressed }) => [
                styles.close,
                Boolean(pending) && styles.disabled,
                pressed && styles.pressed,
              ]}
            >
              <SymbolView name="xmark" tintColor={callColors.text} size={16} />
            </Pressable>
          </View>

          <ScrollView
            contentContainerStyle={styles.content}
            showsVerticalScrollIndicator={false}
          >
            <View style={styles.disclosure}>
              <View style={styles.disclosureIcon}>
                <SymbolView name="phone.fill" tintColor={callColors.success} size={18} />
              </View>
              <View style={styles.disclosureCopy}>
                <Text style={styles.disclosureTitle}>Your call keeps working</Text>
                <Text style={styles.disclosureText}>
                  Audio, video, and room chat continue when every choice below is off. These controls only govern the server copy of your microphone and what may be derived from it.
                </Text>
              </View>
            </View>

            {error ? (
              <View accessibilityLiveRegion="polite" accessibilityRole="alert" style={styles.errorBanner}>
                <SymbolView name="exclamationmark.triangle.fill" tintColor={callColors.warning} size={17} />
                <Text style={styles.errorText}>{error}</Text>
                <Pressable
                  accessibilityLabel="Retry loading microphone data choices"
                  accessibilityRole="button"
                  disabled={loading || Boolean(pending)}
                  onPress={() => void loadStatus()}
                  style={({ pressed }) => [styles.retry, pressed && styles.pressed]}
                >
                  <Text style={styles.retryText}>Retry</Text>
                </Pressable>
              </View>
            ) : null}

            {loading && !status ? (
              <View accessibilityLabel="Loading microphone data choices" style={styles.loadingState}>
                <ActivityIndicator color={callColors.text} />
                <Text style={styles.loadingText}>Loading your choices…</Text>
              </View>
            ) : null}

            {status && !status.storeAvailable ? (
              <View accessibilityRole="alert" style={styles.unavailableBanner}>
                <SymbolView name="lock.fill" tintColor={callColors.warning} size={16} />
                <Text style={styles.unavailableText}>
                  Choices are unavailable, so server capture and all derived uses stay off. Your direct call is unaffected.
                </Text>
                <Pressable
                  accessibilityLabel="Retry loading microphone data choices"
                  accessibilityRole="button"
                  disabled={loading}
                  onPress={() => void loadStatus()}
                  style={({ pressed }) => [styles.retry, pressed && styles.pressed]}
                >
                  {loading ? <ActivityIndicator color={callColors.text} size="small" /> : <Text style={styles.retryText}>Retry</Text>}
                </Pressable>
              </View>
            ) : null}

            {status ? (
              <View style={styles.choiceList}>
                {choices.map((choice) => {
                  const disposition = status.scopes[choice.scope];
                  const effective = status.lanes[choice.scope]?.allowed === true;
                  const offDisposition: ConsentDisposition = disposition === 'granted' ? 'withdrawn' : 'denied';
                  const savingThis = pending?.scope === choice.scope;
                  const controlsDisabled = !status.storeAvailable || !status.choicesMutable || Boolean(pending);
                  const offSelected = disposition === 'denied' || disposition === 'withdrawn';
                  return (
                    <View key={choice.scope} style={styles.choiceCard}>
                      <View style={styles.choiceHeading}>
                        <View style={styles.choiceIcon}>
                          <SymbolView name={choice.icon} tintColor={callColors.text} size={18} />
                        </View>
                        <View style={styles.choiceTitleWrap}>
                          <Text style={styles.choiceTitle}>{choice.title}</Text>
                          <View style={styles.stateRow}>
                            <View style={[
                              styles.stateDot,
                              disposition === 'granted' && styles.stateDotAllowed,
                              offSelected && styles.stateDotOff,
                            ]} />
                            <Text style={styles.stateText}>
                              {dispositionLabel(disposition)}
                              {disposition === 'granted' && !effective ? ' · waiting on an earlier choice' : ''}
                            </Text>
                          </View>
                        </View>
                        {savingThis ? <ActivityIndicator color={callColors.text} size="small" /> : null}
                      </View>
                      <Text style={styles.choiceDetail}>{choice.detail}</Text>
                      <Text style={styles.choiceDependency}>{choice.dependency}</Text>
                      <View accessibilityLabel={`${choice.title} choice`} style={styles.choiceActions}>
                        <Pressable
                          accessibilityLabel={`Allow ${choice.title.toLowerCase()} for this sitting`}
                          accessibilityRole="button"
                          accessibilityState={{
                            busy: savingThis && pending?.disposition === 'granted',
                            disabled: controlsDisabled || disposition === 'granted',
                            selected: disposition === 'granted',
                          }}
                          disabled={controlsDisabled || disposition === 'granted'}
                          onPress={() => void saveChoice(choice.scope, 'granted')}
                          style={({ pressed }) => [
                            styles.choiceButton,
                            disposition === 'granted' && styles.choiceButtonSelected,
                            (controlsDisabled || disposition === 'granted') && disposition !== 'granted' && styles.disabled,
                            pressed && styles.pressed,
                          ]}
                        >
                          {disposition === 'granted' ? (
                            <SymbolView name="checkmark" tintColor={callColors.onSelected} size={13} />
                          ) : null}
                          <Text style={[
                            styles.choiceButtonText,
                            disposition === 'granted' && styles.choiceButtonTextSelected,
                          ]}>Allow</Text>
                        </Pressable>
                        <Pressable
                          accessibilityLabel={disposition === 'granted'
                            ? `Withdraw ${choice.title.toLowerCase()} consent`
                            : `Deny ${choice.title.toLowerCase()} for this sitting`}
                          accessibilityRole="button"
                          accessibilityState={{
                            busy: savingThis && pending?.disposition === offDisposition,
                            disabled: controlsDisabled || offSelected,
                            selected: offSelected,
                          }}
                          disabled={controlsDisabled || offSelected}
                          onPress={() => void saveChoice(choice.scope, offDisposition)}
                          style={({ pressed }) => [
                            styles.choiceButton,
                            offSelected && styles.choiceButtonOffSelected,
                            (controlsDisabled || offSelected) && !offSelected && styles.disabled,
                            pressed && styles.pressed,
                          ]}
                        >
                          <Text style={[
                            styles.choiceButtonText,
                            offSelected && styles.choiceButtonTextOffSelected,
                          ]}>
                            {disposition === 'granted'
                              ? 'Withdraw'
                              : disposition === 'withdrawn' ? 'Withdrawn' : 'Keep off'}
                          </Text>
                        </Pressable>
                      </View>
                    </View>
                  );
                })}
              </View>
            ) : null}

            <Text style={styles.footnote}>
              {status?.policyManaged
                ? 'Internal employee use follows your company rules of the road. External guests control their own choices for each sitting.'
                : 'Choices apply only to this room sitting. A future sitting asks again.'}
            </Text>
          </ScrollView>
        </View>
      </SafeAreaView>
    </Modal>
  );
});

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: callColors.canvas },
  sheet: { flex: 1, backgroundColor: callColors.canvas },
  header: {
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: space[3],
    paddingHorizontal: space[4],
    paddingVertical: space[3],
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: callColors.border,
  },
  headerIdentity: { flex: 1, minWidth: 0, flexDirection: 'row', alignItems: 'center', gap: space[3] },
  headerIcon: {
    width: 42,
    height: 42,
    borderRadius: 14,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: callColors.surface,
  },
  headerCopy: { flex: 1, minWidth: 0 },
  title: { ...type.headline, color: callColors.text },
  subtitle: { ...type.caption, marginTop: 1, color: callColors.textSecondary },
  close: {
    width: hitMin,
    height: hitMin,
    borderRadius: hitMin / 2,
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: callColors.borderControl,
    backgroundColor: callColors.control,
  },
  content: { padding: space[4], paddingBottom: space[8] },
  disclosure: {
    ...shadow.mark,
    flexDirection: 'row',
    gap: space[3],
    padding: space[4],
    borderRadius: radius.xl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: callColors.border,
    backgroundColor: callColors.surface,
  },
  disclosureIcon: {
    width: 38,
    height: 38,
    borderRadius: 13,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: callColors.successSurface,
  },
  disclosureCopy: { flex: 1 },
  disclosureTitle: { ...type.bodyMedium, color: callColors.text },
  disclosureText: { ...type.caption, marginTop: 3, color: callColors.textSecondary },
  errorBanner: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    marginTop: space[3],
    paddingLeft: space[3],
    paddingRight: 5,
    paddingVertical: 5,
    borderRadius: radius.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: callColors.warning,
    backgroundColor: callColors.warningSurface,
  },
  errorText: { ...type.caption, flex: 1, color: callColors.text },
  retry: {
    minWidth: 64,
    minHeight: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.md,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: callColors.borderControl,
    backgroundColor: callColors.control,
  },
  retryText: { ...type.button, color: callColors.text },
  loadingState: { minHeight: 220, alignItems: 'center', justifyContent: 'center', gap: space[3] },
  loadingText: { ...type.bodySm, color: callColors.textSecondary },
  unavailableBanner: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: space[2],
    marginTop: space[3],
    padding: space[3],
    borderRadius: radius.lg,
    backgroundColor: callColors.warningSurface,
  },
  unavailableText: { ...type.caption, flex: 1, color: callColors.textSecondary },
  choiceList: { gap: space[3], marginTop: space[4] },
  choiceCard: {
    padding: space[4],
    borderRadius: radius.xl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: callColors.border,
    backgroundColor: callColors.surface,
  },
  choiceHeading: { flexDirection: 'row', alignItems: 'center', gap: space[3] },
  choiceIcon: {
    width: 38,
    height: 38,
    borderRadius: 13,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: callColors.surface,
  },
  choiceTitleWrap: { flex: 1, minWidth: 0 },
  choiceTitle: { ...type.bodyMedium, color: callColors.text },
  stateRow: { flexDirection: 'row', alignItems: 'center', gap: 5, marginTop: 2 },
  stateDot: { width: 6, height: 6, borderRadius: 3, backgroundColor: callColors.textSecondary },
  stateDotAllowed: { backgroundColor: callColors.success },
  stateDotOff: { backgroundColor: callColors.textSecondary },
  stateText: { ...type.caption, flexShrink: 1, color: callColors.textSecondary },
  choiceDetail: { ...type.bodySm, marginTop: space[3], color: callColors.text },
  choiceDependency: { ...type.caption, marginTop: 2, color: callColors.textSecondary },
  choiceActions: {
    minHeight: hitMin + 8,
    flexDirection: 'row',
    gap: 4,
    marginTop: space[3],
    padding: 4,
    borderRadius: radius.lg,
    backgroundColor: callColors.surface,
  },
  choiceButton: {
    flex: 1,
    minHeight: hitMin,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 5,
    borderRadius: radius.md,
  },
  choiceButtonSelected: { backgroundColor: callColors.selected },
  choiceButtonOffSelected: { borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.borderControl, backgroundColor: callColors.control },
  choiceButtonText: { ...type.button, color: callColors.textSecondary },
  choiceButtonTextSelected: { color: callColors.onSelected },
  choiceButtonTextOffSelected: { color: callColors.text },
  footnote: { ...type.caption, marginTop: space[5], textAlign: 'center', color: callColors.textSecondary },
  disabled: { opacity: 0.42 },
  pressed: { opacity: 0.76, transform: [{ scale: 0.97 }] },
});
