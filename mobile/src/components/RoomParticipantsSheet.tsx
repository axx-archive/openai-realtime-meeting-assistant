import React, { memo, useMemo } from 'react';
import {
  FlatList,
  Modal,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { ink, radius, space, type } from '../theme/tokens';

export type RoomParticipantRow = {
  key: string;
  name: string;
  active: boolean;
  micMuted: boolean;
  screenSharing: boolean;
  videoOff: boolean;
  endpointId?: string;
  local?: boolean;
};

type Props = {
  visible: boolean;
  roomName: string;
  participants: RoomParticipantRow[];
  onClose: () => void;
  onInvite: () => void;
};

function endpointLabel(participant: RoomParticipantRow, duplicateIndex: number): string {
  if (participant.local) return 'This iPhone';
  const endpointId = String(participant.endpointId ?? '').toLocaleLowerCase();
  const device = endpointId.startsWith('ios-')
    ? 'iPhone'
    : endpointId.startsWith('web-') || endpointId.startsWith('desktop-')
      ? 'Desktop web'
      : 'Connected device';
  return duplicateIndex > 0 ? `${device} ${duplicateIndex + 1}` : device;
}

function Status({ icon, label, live = false }: { icon: SFSymbol; label: string; live?: boolean }) {
  return (
    <View style={[styles.status, live && styles.statusLive]}>
      <SymbolView name={icon} tintColor={live ? '#30D158' : 'rgba(255,255,255,0.50)'} size={11} />
      <Text style={[styles.statusText, live && styles.statusTextLive]}>{label}</Text>
    </View>
  );
}

export const RoomParticipantsSheet = memo(function RoomParticipantsSheet({
  visible,
  roomName,
  participants,
  onClose,
  onInvite,
}: Props) {
  const safeArea = useSafeAreaInsets();
  const decoratedParticipants = useMemo(() => {
    const occurrences = new Map<string, number>();
    return participants.map((participant) => {
      const identity = participant.name.trim().toLocaleLowerCase();
      const duplicateIndex = occurrences.get(identity) ?? 0;
      occurrences.set(identity, duplicateIndex + 1);
      return { ...participant, deviceLabel: endpointLabel(participant, duplicateIndex) };
    });
  }, [participants]);

  return (
    <Modal
      animationType="slide"
      onRequestClose={onClose}
      presentationStyle="pageSheet"
      visible={visible}
    >
      <View style={[styles.sheet, { paddingTop: Math.max(safeArea.top, space[3]) }]}>
        <View style={styles.header}>
          <View style={styles.headerCopy}>
            <Text numberOfLines={1} style={styles.title}>People in {roomName}</Text>
            <Text style={styles.subtitle}>{participants.length} connected {participants.length === 1 ? 'device' : 'devices'}</Text>
          </View>
          <Pressable
            accessibilityLabel="Close participants"
            accessibilityRole="button"
            onPress={onClose}
            style={({ pressed }) => [styles.close, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" tintColor="#FFFFFF" size={16} />
          </Pressable>
        </View>

        <Pressable
          accessibilityLabel="Invite someone to this room"
          accessibilityRole="button"
          onPress={onInvite}
          style={({ pressed }) => [styles.invite, pressed && styles.pressed]}
        >
          <View style={styles.inviteIcon}>
            <SymbolView name="person.badge.plus" tintColor={ink[950]} size={18} />
          </View>
          <View style={styles.inviteCopy}>
            <Text style={styles.inviteTitle}>Invite someone</Text>
            <Text style={styles.inviteBody}>Share a secure room link</Text>
          </View>
          <SymbolView name="chevron.right" tintColor="rgba(255,255,255,0.35)" size={13} />
        </Pressable>

        <FlatList
          contentContainerStyle={[styles.listContent, { paddingBottom: Math.max(safeArea.bottom, space[5]) }]}
          data={decoratedParticipants}
          keyExtractor={(item) => item.key}
          renderItem={({ item }) => (
            <View style={styles.participant}>
              <View style={[styles.avatar, item.active && styles.avatarActive]}>
                <Text style={styles.avatarText}>{item.name.trim().slice(0, 1).toLocaleUpperCase() || '?'}</Text>
                {item.active ? <View style={styles.activeDot} /> : null}
              </View>
              <View style={styles.participantCopy}>
                <View style={styles.nameRow}>
                  <Text numberOfLines={1} style={styles.participantName}>{item.local ? `${item.name} (You)` : item.name}</Text>
                  {item.screenSharing ? <Text style={styles.sharingLabel}>SHARING</Text> : null}
                </View>
                <Text style={styles.deviceLabel}>{item.deviceLabel}</Text>
                <View style={styles.statusRow}>
                  <Status icon={item.micMuted ? 'mic.slash.fill' : 'mic.fill'} label={item.micMuted ? 'Muted' : 'Mic on'} />
                  <Status icon={item.videoOff ? 'video.slash.fill' : 'video.fill'} label={item.videoOff ? 'Video off' : 'Video on'} />
                  {item.screenSharing ? <Status icon="rectangle.on.rectangle" label="Presenting" live /> : null}
                </View>
              </View>
            </View>
          )}
          style={styles.list}
        />
      </View>
    </Modal>
  );
});

const styles = StyleSheet.create({
  sheet: { flex: 1, backgroundColor: ink[950] },
  header: { minHeight: 64, flexDirection: 'row', alignItems: 'center', paddingHorizontal: space[4], paddingBottom: space[3] },
  headerCopy: { flex: 1, minWidth: 0 },
  title: { ...type.headline, color: '#FFFFFF' },
  subtitle: { ...type.caption, marginTop: 1, color: 'rgba(255,255,255,0.48)' },
  close: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22, backgroundColor: 'rgba(255,255,255,0.10)' },
  invite: { minHeight: 68, flexDirection: 'row', alignItems: 'center', gap: space[3], marginHorizontal: space[4], marginBottom: space[4], padding: space[3], borderRadius: radius.xl, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.10)', backgroundColor: ink[850] },
  inviteIcon: { width: 42, height: 42, alignItems: 'center', justifyContent: 'center', borderRadius: 15, backgroundColor: '#FFFFFF' },
  inviteCopy: { flex: 1, minWidth: 0 },
  inviteTitle: { ...type.bodyMedium, color: '#FFFFFF' },
  inviteBody: { ...type.caption, color: 'rgba(255,255,255,0.48)' },
  list: { flex: 1 },
  listContent: { gap: space[2], paddingHorizontal: space[4] },
  participant: { minHeight: 88, flexDirection: 'row', alignItems: 'center', gap: space[3], padding: space[3], borderRadius: radius.xl, backgroundColor: ink[850] },
  avatar: { position: 'relative', width: 48, height: 48, alignItems: 'center', justifyContent: 'center', borderRadius: 18, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.10)', backgroundColor: ink[700] },
  avatarActive: { borderColor: 'rgba(48,209,88,0.58)' },
  avatarText: { color: '#FFFFFF', fontSize: 18, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600' },
  activeDot: { position: 'absolute', right: -2, bottom: -2, width: 12, height: 12, borderRadius: 6, borderWidth: 2, borderColor: ink[850], backgroundColor: '#30D158' },
  participantCopy: { flex: 1, minWidth: 0 },
  nameRow: { flexDirection: 'row', alignItems: 'center', gap: 7 },
  participantName: { ...type.bodyMedium, flexShrink: 1, color: '#FFFFFF' },
  sharingLabel: { fontSize: 8, fontFamily: 'GoogleSansFlex_700Bold', fontWeight: '700', letterSpacing: 0.7, color: '#30D158' },
  deviceLabel: { ...type.caption, marginBottom: 5, color: 'rgba(255,255,255,0.43)' },
  statusRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 5 },
  status: { minHeight: 22, flexDirection: 'row', alignItems: 'center', gap: 4, paddingHorizontal: 7, borderRadius: 9, backgroundColor: 'rgba(255,255,255,0.07)' },
  statusLive: { backgroundColor: 'rgba(48,209,88,0.12)' },
  statusText: { fontSize: 9, fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500', lineHeight: 12, color: 'rgba(255,255,255,0.50)' },
  statusTextLive: { color: '#30D158' },
  pressed: { opacity: 0.76, transform: [{ scale: 0.98 }] },
});
