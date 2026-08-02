import React from 'react';
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import type {
  StrideMeetingSpecialistInvitation,
  StrideMeetingSpecialistStatus,
} from '../api/types';
import { colors, radius, shadow, space, type } from '../theme/tokens';

type Props = {
  error: string | null;
  loading: boolean;
  onClose: () => void;
  onRequest: (agentId: string, displayName: string) => void;
  onResolve: (invitation: StrideMeetingSpecialistInvitation, decision: 'approved' | 'declined' | 'dismissed') => void;
  pending: boolean;
  status: StrideMeetingSpecialistStatus | null;
  visible: boolean;
};

function unavailableCopy(reason?: string): string {
  switch (reason) {
    case 'provider_qualification_pending': return 'Your agent roster is ready. Voice joining remains off until its provider route passes qualification.';
    case 'no_eligible_specialists': return 'No hired agent is currently eligible for this meeting.';
    case 'active_member_room_required': return 'Join this room as a team member to manage agents.';
    case 'consent_unavailable': return 'Agent invitations are paused until everyone’s meeting consent can be verified.';
    case 'state_restore_failed': return 'Agent invitations are unavailable while protected state is recovered.';
    default: return 'Meeting agents are not enabled for this workspace yet.';
  }
}

const contextLabels: Record<string, string> = {
  meeting_transcript: 'Live meeting transcript',
  meeting_analysis: 'Meeting analysis',
  company_brain: 'Relevant company brain context',
  active_work: 'Active work state',
};

function durationLabel(seconds: number | undefined): string {
  const value = Math.max(0, Math.trunc(Number(seconds) || 0));
  return value > 0 && value % 60 === 0 ? `${value / 60} min` : `${value} sec`;
}

function numberLabel(value: number | undefined): string {
  return Math.max(0, Math.trunc(Number(value) || 0)).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

function contextSummary(classes: string[]): string {
  return (Array.isArray(classes) ? classes : [])
    .map((value) => contextLabels[value] || value.replaceAll('_', ' '))
    .filter(Boolean)
    .join(' · ') || 'No additional context';
}

function audienceSummary(invitation: StrideMeetingSpecialistInvitation): string {
  const count = Array.isArray(invitation.audience?.principals) ? invitation.audience.principals.length : 0;
  return `${numberLabel(count)} current meeting participant${count === 1 ? '' : 's'}`;
}

function expectedSummary(invitation: StrideMeetingSpecialistInvitation): string {
  return `About ${durationLabel(invitation.expectedTimeSeconds)} · up to $${(Math.max(0, Number(invitation.expectedCostCents) || 0) / 100).toFixed(2)}`;
}

function limitsSummary(invitation: StrideMeetingSpecialistInvitation): string {
  const limits = invitation.hardLimits;
  return `${numberLabel(limits?.turnBudget)} turns · ${durationLabel(limits?.audioBudgetSeconds)} audio · ${numberLabel(limits?.tokenBudget)} tokens · ${durationLabel(limits?.maxFloorLeaseSeconds)} max floor`;
}

export function RoomSpecialistsSheet({ error, loading, onClose, onRequest, onResolve, pending, status, visible }: Props) {
  const insets = useSafeAreaInsets();
  return (
    <Modal animationType="slide" onRequestClose={onClose} presentationStyle="pageSheet" visible={visible}>
      <View style={[styles.screen, { paddingTop: Math.max(insets.top, space[4]), paddingBottom: Math.max(insets.bottom, space[4]) }]}>
        <View style={styles.header}>
          <View style={styles.headerCopy}>
            <Text style={styles.eyebrow}>YOUR AGENT TEAM</Text>
            <Text style={styles.title}>Bring a specialist in</Text>
            <Text style={styles.subtitle}>Everyone sees who is invited. An agent never joins until a person approves the exact invitation.</Text>
          </View>
          <Pressable accessibilityLabel="Close meeting agents" accessibilityRole="button" onPress={onClose} style={({ pressed }) => [styles.close, pressed && styles.pressed]}>
            <Text style={styles.closeText}>Done</Text>
          </Pressable>
        </View>
        <ScrollView contentContainerStyle={styles.content}>
          {loading ? <View style={styles.loading}><ActivityIndicator color={colors.text1} /><Text style={styles.body}>Checking this meeting…</Text></View> : null}
          {error ? <View style={styles.noticeError}><Text style={styles.noticeTitle}>Couldn’t load agents</Text><Text style={styles.body}>{error}</Text></View> : null}
          {!loading && !error && status ? (
            <>
              <View style={styles.notice}>
                <View style={styles.statusDot} />
                <View style={styles.noticeCopy}>
                  <Text style={styles.noticeTitle}>{status.available ? 'Available' : 'Protected by default'}</Text>
                  <Text style={styles.body}>{unavailableCopy(status.reason)}</Text>
                </View>
              </View>
              {status.invitations.map((invitation) => (
                <View key={invitation.id} style={[styles.card, shadow[1]]}>
                  <Text style={styles.cardTitle}>{invitation.displayName || invitation.agentId}</Text>
                  <Text style={styles.cardMeta}>{invitation.status.replaceAll('_', ' ')}</Text>
                  <Text style={styles.purpose}>{invitation.purposeSummary || 'Specialist contribution requested'}</Text>
                  <Text style={styles.approvalState}>
                    {invitation.providerSessionStarted
                      ? 'Voice session active under these limits.'
                      : invitation.decision === 'approved'
                        ? 'Approved. Voice remains fenced until the server starts this exact session.'
                        : 'Review the shared context, audience, and hard limits before deciding.'}
                  </Text>
                  <View accessibilityLabel="Shared context, audience, expected use, and hard limits" style={styles.details}>
                    <View style={styles.detailWide}><Text style={styles.detailLabel}>SHARES</Text><Text style={styles.detailValue}>{contextSummary(invitation.contextClasses)}</Text></View>
                    <View style={styles.detail}><Text style={styles.detailLabel}>AUDIENCE</Text><Text style={styles.detailValue}>{audienceSummary(invitation)}</Text></View>
                    <View style={styles.detail}><Text style={styles.detailLabel}>EXPECTED</Text><Text style={styles.detailValue}>{expectedSummary(invitation)}</Text></View>
                    <View style={styles.detailWide}><Text style={styles.detailLabel}>HARD LIMITS</Text><Text style={styles.detailValue}>{limitsSummary(invitation)}</Text></View>
                  </View>
                  {invitation.status === 'awaiting_approval' ? (
                    <View style={styles.actions}>
                      <Pressable accessibilityRole="button" disabled={pending} onPress={() => onResolve(invitation, 'declined')} style={({ pressed }) => [styles.secondary, pressed && styles.pressed]}><Text style={styles.secondaryText}>Decline</Text></Pressable>
                      <Pressable accessibilityRole="button" disabled={pending} onPress={() => onResolve(invitation, 'approved')} style={({ pressed }) => [styles.primary, pressed && styles.pressed]}><Text style={styles.primaryText}>Approve</Text></Pressable>
                    </View>
                  ) : invitation.decision === 'approved' ? (
                    <Pressable accessibilityRole="button" disabled={pending} onPress={() => onResolve(invitation, 'dismissed')} style={({ pressed }) => [styles.secondary, styles.dismiss, pressed && styles.pressed]}><Text style={styles.secondaryText}>Dismiss agent</Text></Pressable>
                  ) : null}
                </View>
              ))}
              {status.candidates.filter((candidate) => !status.invitations.some((invitation) => invitation.agentId === candidate.agentId && (invitation.decision === 'requested' || invitation.decision === 'approved'))).map((candidate) => (
                <View key={candidate.agentId} style={[styles.card, shadow[1]]}>
                  <Text style={styles.cardTitle}>{candidate.displayName || candidate.agentId}</Text>
                  <Text style={styles.body}>A hired teammate eligible for this room. Context and live audio remain consent-scoped.</Text>
                  <Pressable accessibilityRole="button" disabled={!status.canInvite || pending} onPress={() => onRequest(candidate.agentId, candidate.displayName || candidate.agentId)} style={({ pressed }) => [styles.primary, (!status.canInvite || pending) && styles.disabled, pressed && styles.pressed]}>
                    <Text style={styles.primaryText}>Request invitation</Text>
                  </Pressable>
                </View>
              ))}
              {!status.candidates.length && !status.invitations.length ? <Text style={styles.empty}>No meeting agents to show.</Text> : null}
            </>
          ) : null}
        </ScrollView>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: colors.bgApp, paddingHorizontal: space[5] },
  header: { flexDirection: 'row', alignItems: 'flex-start', gap: space[3], paddingBottom: space[4] },
  headerCopy: { flex: 1 },
  eyebrow: { ...type.label, color: colors.text3, letterSpacing: 1.2 },
  title: { ...type.title2, color: colors.text1, marginTop: space[1] },
  subtitle: { ...type.bodySm, color: colors.text2, marginTop: space[2] },
  close: { minWidth: 48, minHeight: 44, justifyContent: 'center', alignItems: 'flex-end' },
  closeText: { ...type.button, color: colors.accent },
  content: { gap: space[3], paddingBottom: space[6] },
  loading: { minHeight: 96, alignItems: 'center', justifyContent: 'center', gap: space[2] },
  notice: { flexDirection: 'row', gap: space[3], borderRadius: radius.xl, backgroundColor: colors.surface2, padding: space[4] },
  noticeError: { borderRadius: radius.xl, backgroundColor: colors.dangerSoft, padding: space[4] },
  noticeCopy: { flex: 1 },
  noticeTitle: { ...type.bodyMedium, color: colors.text1 },
  statusDot: { width: 9, height: 9, borderRadius: 5, backgroundColor: colors.text3, marginTop: 5 },
  body: { ...type.bodySm, color: colors.text2, marginTop: 3 },
  card: { borderRadius: radius.xl, backgroundColor: colors.surface1, padding: space[4], gap: space[3] },
  cardTitle: { ...type.headline, color: colors.text1 },
  cardMeta: { ...type.caption, color: colors.text2, textTransform: 'capitalize' },
  purpose: { ...type.bodyMedium, color: colors.text1 },
  approvalState: { ...type.bodySm, color: colors.text2 },
  details: { flexDirection: 'row', flexWrap: 'wrap', gap: space[3], borderRadius: radius.lg, backgroundColor: colors.surface2, padding: space[3] },
  detail: { flexGrow: 1, flexBasis: '44%', minWidth: 120 },
  detailWide: { flexBasis: '100%' },
  detailLabel: { ...type.label, color: colors.text3, fontSize: 10, lineHeight: 12, letterSpacing: 0.8 },
  detailValue: { ...type.bodySm, color: colors.text2, marginTop: 3, fontVariant: ['tabular-nums'] },
  actions: { flexDirection: 'row', gap: space[2] },
  primary: { minHeight: 44, flex: 1, borderRadius: radius.lg, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.accent, paddingHorizontal: space[4] },
  primaryText: { ...type.button, color: colors.onAccent },
  secondary: { minHeight: 44, flex: 1, borderRadius: radius.lg, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.surface3, paddingHorizontal: space[4] },
  secondaryText: { ...type.button, color: colors.text1 },
  dismiss: { flex: 0 },
  disabled: { opacity: 0.45 },
  pressed: { transform: [{ scale: 0.96 }] },
  empty: { ...type.body, color: colors.text2, textAlign: 'center', paddingVertical: space[6] },
});
