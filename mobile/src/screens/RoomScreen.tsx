import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActionSheetIOS,
  ActivityIndicator,
  Alert,
  FlatList,
  NativeModules,
  Platform,
  Pressable,
  Share,
  StyleSheet,
  Text,
  View,
  findNodeHandle,
  useWindowDimensions,
  type LayoutChangeEvent,
  type StyleProp,
  type ViewStyle,
} from 'react-native';
import { useKeepAwake } from 'expo-keep-awake';
import * as Haptics from 'expo-haptics';
import { StatusBar } from 'expo-status-bar';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { RTCView, ScreenCapturePickerView } from 'react-native-webrtc';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { api, BonfireApiError } from '../api/client';
import type { Room, RoomAgentParticipant } from '../api/types';
import type { StrideMeetingSpecialistStatus, StrideMeetingSpecialistInvitation } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { Screen } from '../components/Screen';
import {
  RoomConversationSheet,
  type RoomConversationMode,
} from '../components/RoomConversationSheet';
import { RoomParticipantsSheet, type RoomParticipantRow } from '../components/RoomParticipantsSheet';
import { AgentSpeakingWaveform } from '../components/AgentSpeakingWaveform';
import { RoomSpecialistsSheet } from '../components/RoomSpecialistsSheet';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { useNativeRoom } from '../realtime/useNativeRoom';
import {
  cameraFramingRenderRevision,
  centerStageControlStatus,
} from '../realtime/cameraFramingLifecycle';
import {
  focusedVideoParticipant,
  pictureInPictureParticipant,
  participantVideoAccessibilityStatus,
  pinnedVideoParticipantIsStale,
  presentRemoteParticipantDevices,
  presentRemoteVideoParticipants,
  type PresentedVideoParticipant,
  videoStageParticipants,
} from '../realtime/callPresentation';
import { normalizedParticipantName } from '../realtime/participantMedia';
import type { RootStackParamList } from '../navigation/types';
import { colors, ink, radius, shadow, space, type } from '../theme/tokens';
import { API_BASE_URL } from '../config';
import { firstArray } from '../utils/records';
import {
  containedVideoLabelPosition,
  fittedVideoDimensions,
  nativeVideoRenderIdentity,
  type VideoDimensions,
} from '../utils/videoLayout';

type Props = NativeStackScreenProps<RootStackParamList, 'Room'>;
type ScreenCapturePickerHandle = React.ElementRef<typeof ScreenCapturePickerView>;
type InCallActionDescriptor = {
  id: string;
  label: string;
  onSelect?: () => void;
  cancel?: boolean;
  destructive?: boolean;
  disabled?: boolean;
};

type CallParticipant = PresentedVideoParticipant;

const remotePIPOptions = {
  enabled: true,
  preferredSize: { width: 360, height: 202 },
  startAutomatically: true,
  stopAutomatically: true,
} as const;

function participantInitials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (!words.length) return '?';
  return words.slice(0, 2).map((word) => word[0]?.toUpperCase()).join('');
}

function agentVoiceLabel(agent: RoomAgentParticipant): string {
  if (agent.status === 'degraded' || agent.voiceState === 'degraded') return 'Connection interrupted';
  switch (agent.voiceState) {
    case 'talking': return 'Speaking';
    case 'hearing': return 'Listening to the room';
    case 'thinking': return 'Thinking';
    case 'listening': return 'Ready';
    default: return 'Joining';
  }
}

/** Isolated child keeps hook order stable while the call surface opts in. */
function LiveVideoWakeLock() {
  useKeepAwake('bonfire-room-video');
  return null;
}

type CallVideoTileProps = {
  participant: CallParticipant;
  primary?: boolean;
  mirror?: boolean;
  compact?: boolean;
  pinned?: boolean;
  pictureInPicture?: boolean;
  fit?: 'contain' | 'cover';
  labelBottomClearance?: number;
  onVideoDimensionsChange?: (dimensions: VideoDimensions) => void;
  onPress?: () => void;
  style?: StyleProp<ViewStyle>;
};

const CallVideoTile = memo(function CallVideoTile({
  participant,
  primary = false,
  mirror = false,
  compact = false,
  pinned = false,
  pictureInPicture = false,
  fit = 'cover',
  labelBottomClearance = 0,
  onVideoDimensionsChange,
  onPress,
  style,
}: CallVideoTileProps) {
  const { name, streamURL, active, videoOff } = participant;
  const [containerDimensions, setContainerDimensions] = useState<VideoDimensions | null>(null);
  const [measuredVideo, setMeasuredVideo] = useState<(VideoDimensions & { streamURL: string }) | null>(null);
  const videoDimensions = measuredVideo?.streamURL === streamURL ? measuredVideo : null;
  const fittedLabelPosition = useMemo(() => {
    if (fit !== 'contain' || !containerDimensions || !videoDimensions) return null;
    const position = containedVideoLabelPosition(containerDimensions, videoDimensions, space[3]);
    if (!position) return null;
    return {
      left: position.left,
      bottom: Math.max(position.bottom, labelBottomClearance),
      maxWidth: position.maxWidth,
    };
  }, [containerDimensions, fit, labelBottomClearance, videoDimensions]);
  const waitingForContainedVideo = Boolean(streamURL && fit === 'contain' && !fittedLabelPosition);
  const labelPosition = fittedLabelPosition
    ?? (labelBottomClearance > 0 ? { bottom: labelBottomClearance } : null);
  const handleLayout = useCallback((event: LayoutChangeEvent) => {
    const { width, height } = event.nativeEvent.layout;
    setContainerDimensions((current) => current?.width === width && current.height === height
      ? current
      : { width, height });
  }, []);
  const handleVideoDimensionsChange = useCallback((event: { nativeEvent: VideoDimensions }) => {
    if (!streamURL) return;
    const { width, height } = event.nativeEvent;
    setMeasuredVideo((current) => current?.streamURL === streamURL && current.width === width && current.height === height
      ? current
      : { streamURL, width, height });
    onVideoDimensionsChange?.({ width, height });
  }, [onVideoDimensionsChange, streamURL]);
  return (
    <Pressable
      accessible
      accessibilityHint={onPress
        ? pinned
          ? 'Unpins this person and resumes following the active speaker'
          : 'Pins this person to the main stage'
        : undefined}
      accessibilityLabel={`${name}${active ? ', speaking' : ''}, ${participantVideoAccessibilityStatus(participant)}`}
      accessibilityRole={onPress ? 'button' : undefined}
      disabled={!onPress}
      onLayout={fit === 'contain' ? handleLayout : undefined}
      onPress={onPress}
      style={({ pressed }) => [
        styles.callVideoTile,
        compact && styles.callVideoTileCompact,
        participant.screenSharing && styles.callVideoTileSharing,
        style,
        pressed && styles.callVideoTilePressed,
      ]}
    >
      {streamURL ? (
        <RTCView
          iosPIP={pictureInPicture && !mirror ? remotePIPOptions : undefined}
          mirror={mirror}
          objectFit={fit}
          onDimensionsChange={fit === 'contain' ? handleVideoDimensionsChange : undefined}
          streamURL={streamURL}
          style={styles.callVideo}
          zOrder={primary ? 0 : 1}
        />
      ) : (
        <View style={styles.videoPlaceholder}>
          <View style={[styles.videoAvatar, compact && styles.videoAvatarCompact]}>
            <Text style={[styles.videoAvatarText, compact && styles.videoAvatarTextCompact]}>{participantInitials(name)}</Text>
          </View>
          {!compact ? (
            <View style={styles.cameraOffRow}>
              <SymbolView name="video.slash.fill" tintColor="rgba(255,255,255,0.55)" size={13} />
              <Text style={styles.cameraOffText}>{videoOff ? 'Video off' : 'Video unavailable'}</Text>
            </View>
          ) : null}
        </View>
      )}
      <View
        accessible={false}
        pointerEvents="none"
        style={[
          styles.callVideoLabel,
          compact && styles.callVideoLabelCompact,
          labelPosition,
          waitingForContainedVideo && styles.callVideoLabelMeasuring,
        ]}
      >
        {pinned ? <SymbolView name="pin.fill" tintColor="#FFFFFF" size={compact ? 9 : 10} /> : active ? <View style={styles.speakerDot} /> : null}
        <Text numberOfLines={1} style={[styles.callVideoLabelText, compact && styles.callVideoLabelTextCompact]}>
          {participant.screenSharing ? `${name} · Sharing` : pinned ? `${name} · Pinned` : name}
        </Text>
      </View>
      {participant.screenSharing && !compact ? (
        <View accessible accessibilityLabel={`${name} is sharing their screen`} style={styles.screenShareBadge}>
          <SymbolView name="rectangle.on.rectangle" tintColor="#30D158" size={12} />
          <Text style={styles.screenShareBadgeText}>PRESENTING</Text>
        </View>
      ) : null}
    </Pressable>
  );
});

const RoomAgentBench = memo(function RoomAgentBench({
  agents,
  bottom,
}: {
  agents: RoomAgentParticipant[];
  bottom: number;
}) {
  const renderAgent = useCallback(({ item }: { item: RoomAgentParticipant }) => {
    const speaking = item.voiceState === 'talking';
    return (
      <View
        accessible
        accessibilityLabel={`${item.name}, agent in the room, ${agentVoiceLabel(item)}`}
        style={[styles.agentBenchMember, speaking && styles.agentBenchMemberSpeaking]}
      >
        <View style={styles.agentBenchIdentity}>
          <View style={[styles.agentBenchDot, { backgroundColor: item.color }]} />
          <View style={styles.agentBenchCopy}>
            <Text numberOfLines={1} style={styles.agentBenchName}>{item.name}</Text>
            <Text numberOfLines={1} style={[styles.agentBenchState, speaking && { color: item.color }]}>{agentVoiceLabel(item)}</Text>
          </View>
        </View>
        <AgentSpeakingWaveform color={item.color} mini speaking={speaking} />
      </View>
    );
  }, []);

  if (!agents.length) return null;
  return (
    <View style={[styles.agentBench, { bottom }]}>
      <View style={styles.agentBenchHeading}>
        <SymbolView name="waveform" tintColor="rgba(255,255,255,0.68)" size={12} />
        <Text style={styles.agentBenchLabel}>AGENTS IN THE ROOM</Text>
      </View>
      <FlatList
        contentContainerStyle={styles.agentBenchList}
        data={agents}
        horizontal
        keyExtractor={(agent) => `${agent.id}:${agent.invitationId}`}
        renderItem={renderAgent}
        showsHorizontalScrollIndicator={false}
      />
    </View>
  );
});

type LocalPreviewProps = {
  name: string;
  streamURL?: string;
  cameraOff: boolean;
  suspended: boolean;
  framingRevision?: string;
  screenSharing?: boolean;
  videoTrackId?: string;
  bottom?: number;
  inline?: boolean;
  onSwitchCamera?: () => void;
  style?: StyleProp<ViewStyle>;
};

const LocalPreview = memo(function LocalPreview({ name, streamURL, cameraOff, suspended, framingRevision = '', screenSharing = false, videoTrackId, bottom, inline = false, onSwitchCamera, style }: LocalPreviewProps) {
  // Outbound publication recovery must not blank a healthy local capture.
  const videoVisible = Boolean(streamURL && (screenSharing || !cameraOff));
  const renderIdentity = nativeVideoRenderIdentity(
    streamURL,
    videoTrackId,
    screenSharing ? 'screen' : 'camera',
    screenSharing ? '' : framingRevision,
  );
  const [rendererState, setRendererState] = useState({ identity: '', ready: false });
  const rendererReady = rendererState.identity === renderIdentity && rendererState.ready;
  const handleRendererDimensions = useCallback((event: { nativeEvent: VideoDimensions }) => {
    const { width, height } = event.nativeEvent;
    if (width <= 0 || height <= 0) return;
    setRendererState({ identity: renderIdentity, ready: true });
  }, [renderIdentity]);
  return (
    <View
      accessible={false}
      style={[styles.localPreview, inline && styles.localPreviewInline, !inline && { bottom }, style]}
    >
      <View
        accessible
        accessibilityLabel={`Your preview, ${screenSharing ? 'sharing screen' : suspended ? 'camera restoring' : cameraOff ? 'camera off' : 'video on'}`}
        style={StyleSheet.absoluteFill}
      >
        {videoVisible ? (
          <>
            <RTCView
              key={renderIdentity}
              mirror={!screenSharing}
              objectFit={screenSharing ? 'contain' : 'cover'}
              onDimensionsChange={handleRendererDimensions}
              streamURL={streamURL}
              style={styles.callVideo}
              zOrder={2}
            />
            {!rendererReady ? (
              <View pointerEvents="none" style={styles.localPreviewStarting}>
                <ActivityIndicator color="#FFFFFF" size="small" />
                <Text style={styles.localPreviewStartingText}>{screenSharing ? 'Starting share…' : 'Starting video…'}</Text>
              </View>
            ) : null}
          </>
        ) : (
          <View style={styles.localPlaceholder}>
            <Text style={styles.localAvatarText}>{participantInitials(name)}</Text>
            <SymbolView name={suspended ? 'arrow.clockwise' : 'video.slash.fill'} tintColor="rgba(255,255,255,0.64)" size={14} />
          </View>
        )}
        <View style={styles.localLabelPill}>
          <Text numberOfLines={1} style={styles.localLabelText}>{screenSharing ? 'Sharing' : suspended ? 'Restoring' : 'You'}</Text>
        </View>
      </View>
      {videoVisible && !screenSharing && onSwitchCamera ? (
        <Pressable
          accessibilityLabel="Switch camera"
          accessibilityRole="button"
          onPress={() => {
            void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
            onSwitchCamera();
          }}
          style={({ pressed }) => [styles.switchCamera, pressed && styles.switchCameraPressed]}
        >
          <SymbolView name="camera.rotate.fill" tintColor="#FFFFFF" size={15} />
        </Pressable>
      ) : null}
    </View>
  );
});

type CallControlProps = {
  icon: SFSymbol;
  label: string;
  accessibilityLabel: string;
  onPress: () => void;
  tone?: 'default' | 'off' | 'recording' | 'danger';
  disabled?: boolean;
  badge?: number;
};

const CallControl = memo(function CallControl({
  icon,
  label,
  accessibilityLabel,
  onPress,
  tone = 'default',
  disabled = false,
  badge = 0,
}: CallControlProps) {
  const inverted = tone === 'off';
  const tintColor = inverted
    ? ink[950]
    : tone === 'recording'
      ? '#FF6B63'
      : '#FFFFFF';
  const handlePress = useCallback(() => {
    void Haptics.impactAsync(tone === 'danger'
      ? Haptics.ImpactFeedbackStyle.Medium
      : Haptics.ImpactFeedbackStyle.Light);
    onPress();
  }, [onPress, tone]);
  return (
    <Pressable
      accessibilityLabel={accessibilityLabel}
      accessibilityRole="button"
      accessibilityState={{ disabled }}
      disabled={disabled}
      onPress={handlePress}
      style={({ pressed }) => [
        styles.callControl,
        tone === 'off' && styles.callControlOff,
        tone === 'recording' && styles.callControlRecording,
        tone === 'danger' && styles.callControlDanger,
        disabled && styles.callControlDisabled,
        pressed && styles.callControlPressed,
      ]}
    >
      <SymbolView name={icon} tintColor={tintColor} size={20} />
      <Text numberOfLines={1} style={[styles.callControlLabel, inverted && styles.callControlLabelInverted]}>{label}</Text>
      {badge > 0 ? (
        <View style={styles.callControlBadge}>
          <Text style={styles.callControlBadgeText}>{badge > 99 ? '99+' : badge}</Text>
        </View>
      ) : null}
    </Pressable>
  );
});

type CallLayoutProps = {
  participants: CallParticipant[];
  audioParticipantCount: number;
  pinnedParticipantKey: string | null;
  pictureInPictureParticipantKey: string | null;
  localName: string;
  localStreamURL?: string;
  localVideoTrackId?: string;
  localScreenShareURL?: string;
  localScreenShareTrackId?: string;
  localScreenSharing: boolean;
  localVideoVisible: boolean;
  localCameraOff: boolean;
  localVideoSuspended: boolean;
  localCameraFramingRevision: string;
  landscape: boolean;
  bottomInset: number;
  agentBenchVisible: boolean;
  onSwitchCamera?: () => void;
  onSelectParticipant: (participant: CallParticipant) => void;
};

const CallLayout = memo(function CallLayout({
  participants,
  audioParticipantCount,
  pinnedParticipantKey,
  pictureInPictureParticipantKey,
  localName,
  localStreamURL,
  localVideoTrackId,
  localScreenShareURL,
  localScreenShareTrackId,
  localScreenSharing,
  localVideoVisible,
  localCameraOff,
  localVideoSuspended,
  localCameraFramingRevision,
  landscape,
  bottomInset,
  agentBenchVisible,
  onSwitchCamera,
  onSelectParticipant,
}: CallLayoutProps) {
  const remoteCount = participants.length;
  const localParticipant = useMemo<CallParticipant>(() => ({
    key: `local-stage:${localScreenSharing ? localScreenShareTrackId ?? 'pending' : `${localVideoTrackId ?? 'pending'}:${localCameraFramingRevision}`}`,
    name: 'You',
    streamURL: localScreenSharing ? localScreenShareURL : localVideoVisible ? localStreamURL : undefined,
    active: false,
    micMuted: false,
    screenSharing: localScreenSharing,
    videoOff: localScreenSharing ? false : localCameraOff,
  }), [localCameraFramingRevision, localCameraOff, localScreenShareTrackId, localScreenShareURL, localScreenSharing, localStreamURL, localVideoTrackId, localVideoVisible]);
  const dockClearance = bottomInset + 96 + (agentBenchVisible ? 70 : 0);
  const [stageSlotDimensions, setStageSlotDimensions] = useState<VideoDimensions | null>(null);
  const [stageVideoMeasurement, setStageVideoMeasurement] = useState<(VideoDimensions & { identity: string }) | null>(null);
  const stageParticipant = participants[0];
  const stageIsScreenShare = Boolean(stageParticipant?.screenSharing);
  const stageMediaIdentity = `${stageParticipant?.key ?? 'none'}:${stageParticipant?.streamURL ?? ''}`;
  const stageVideoDimensions = stageVideoMeasurement?.identity === stageMediaIdentity
    ? stageVideoMeasurement
    : { width: 16, height: 9 };
  const stageTileDimensions = stageSlotDimensions
    ? fittedVideoDimensions(stageSlotDimensions, stageVideoDimensions)
    : null;
  const handleStageSlotLayout = useCallback((event: LayoutChangeEvent) => {
    const { width, height } = event.nativeEvent.layout;
    setStageSlotDimensions((current) => current?.width === width && current.height === height
      ? current
      : { width, height });
  }, []);
  const handleStageVideoDimensions = useCallback((dimensions: VideoDimensions) => {
    setStageVideoMeasurement((current) => current?.identity === stageMediaIdentity
      && current.width === dimensions.width
      && current.height === dimensions.height
      ? current
      : { identity: stageMediaIdentity, ...dimensions });
  }, [stageMediaIdentity]);

  const renderStripParticipant = useCallback(({ item }: { item: CallParticipant }) => (
    <CallVideoTile
      compact
      fit={item.screenSharing ? 'contain' : 'cover'}
      onPress={() => onSelectParticipant(item)}
      participant={item}
      pictureInPicture={item.key === pictureInPictureParticipantKey}
      pinned={item.key === pinnedParticipantKey}
      style={[styles.participantStripTile, landscape && styles.participantStripTileLandscape]}
    />
  ), [landscape, onSelectParticipant, pictureInPictureParticipantKey, pinnedParticipantKey]);

  if (remoteCount === 0) {
    if (localParticipant.streamURL) {
      return <CallVideoTile key={localParticipant.key} fit={localScreenSharing ? 'contain' : 'cover'} mirror={!localScreenSharing} participant={localParticipant} style={styles.oneUpTile} />;
    }
    return (
      <View
        accessible
        accessibilityLabel={`Audio-only room, ${audioParticipantCount} ${audioParticipantCount === 1 ? 'participant' : 'participants'}. No participant video is on.`}
        accessibilityRole="summary"
        style={styles.audioOnlyStage}
      >
        <View style={styles.audioOnlyMark}>
          <SymbolView name="waveform" tintColor="rgba(255,255,255,0.72)" size={28} />
        </View>
        <Text style={styles.audioOnlyTitle}>Audio only</Text>
        <Text style={styles.audioOnlyCopy}>{audioParticipantCount} here · cameras are off</Text>
      </View>
    );
  }

  if (remoteCount === 1) {
    if (!participants[0].screenSharing) {
      return (
        <CallVideoTile
          fit="cover"
          labelBottomClearance={dockClearance}
          onPress={() => onSelectParticipant(participants[0])}
          participant={participants[0]}
          pictureInPicture={participants[0].key === pictureInPictureParticipantKey}
          pinned={participants[0].key === pinnedParticipantKey}
          primary
          style={styles.oneUpTile}
        />
      );
    }
    return (
      <View onLayout={handleStageSlotLayout} style={styles.oneUpStageSlot}>
        <CallVideoTile
          fit="contain"
          onPress={() => onSelectParticipant(participants[0])}
          onVideoDimensionsChange={handleStageVideoDimensions}
          participant={participants[0]}
          pictureInPicture={participants[0].key === pictureInPictureParticipantKey}
          pinned={participants[0].key === pinnedParticipantKey}
          primary
          style={[styles.oneUpPrimaryTile, stageTileDimensions]}
        />
      </View>
    );
  }

  return (
    <View style={[styles.largeCallShell, { paddingBottom: dockClearance }, landscape && styles.largeCallShellLandscape]}>
      <View onLayout={handleStageSlotLayout} style={styles.primaryStageSlot}>
        <CallVideoTile
          fit={stageIsScreenShare ? 'contain' : 'cover'}
          onPress={() => onSelectParticipant(participants[0])}
          onVideoDimensionsChange={stageIsScreenShare ? handleStageVideoDimensions : undefined}
          participant={participants[0]}
          pictureInPicture={participants[0].key === pictureInPictureParticipantKey}
          pinned={participants[0].key === pinnedParticipantKey}
          primary
          style={stageIsScreenShare
            ? [styles.largePrimaryTile, stageTileDimensions]
            : styles.largePrimaryCameraTile}
        />
      </View>
      <View style={[styles.participantRail, landscape && styles.participantRailLandscape]}>
        {localScreenSharing || localVideoVisible ? (
          <LocalPreview
            cameraOff={localCameraOff}
            framingRevision={localCameraFramingRevision}
            inline
            name={localName}
            onSwitchCamera={onSwitchCamera}
            screenSharing={localScreenSharing}
            streamURL={localScreenSharing ? localScreenShareURL : localStreamURL}
            style={[styles.participantStripTile, landscape && styles.participantStripTileLandscape]}
            suspended={localVideoSuspended}
            videoTrackId={localScreenSharing ? localScreenShareTrackId : localVideoTrackId}
          />
        ) : null}
        <FlatList
          contentContainerStyle={[styles.participantStripContent, landscape && styles.participantStripContentLandscape]}
          data={participants.slice(1)}
          horizontal={!landscape}
          keyExtractor={(item) => item.key}
          renderItem={renderStripParticipant}
          showsHorizontalScrollIndicator={false}
          showsVerticalScrollIndicator={false}
          style={[styles.participantStrip, landscape && styles.participantStripLandscape]}
        />
      </View>
    </View>
  );
});

export function RoomScreen({ route, navigation }: Props) {
  const { sessionToken, user } = useAuth();
  const office = useOfficeEvents();
  const nativeRoom = useNativeRoom(sessionToken, route.params.roomId, {
    email: user?.email,
    name: user?.name,
  });
  const safeArea = useSafeAreaInsets();
  const window = useWindowDimensions();
  const [room, setRoom] = useState<Room | null>(null);
  const [participants, setParticipants] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [guestLinks, setGuestLinks] = useState<Array<{ id: string; label?: string; expiresAt?: string }>>([]);
  const [pinnedParticipantKey, setPinnedParticipantKey] = useState<string | null>(null);
  const [conversationMode, setConversationMode] = useState<RoomConversationMode>('chat');
  const [conversationVisible, setConversationVisible] = useState(false);
  const [participantsVisible, setParticipantsVisible] = useState(false);
  const [specialistsVisible, setSpecialistsVisible] = useState(false);
  const [specialists, setSpecialists] = useState<StrideMeetingSpecialistStatus | null>(null);
  const [agentControlSnapshot, setAgentControlSnapshot] = useState<RoomAgentParticipant[]>([]);
  const [specialistsLoading, setSpecialistsLoading] = useState(false);
  const [specialistsPending, setSpecialistsPending] = useState(false);
  const [specialistsError, setSpecialistsError] = useState<string | null>(null);
  const allowCallNavigationRef = useRef(false);
  const screenCapturePickerRef = useRef<ScreenCapturePickerHandle | null>(null);
  const wideFramingExplainedRef = useRef(false);
  const framingActionPendingRef = useRef(false);
  const cameraFramingRef = useRef(nativeRoom.state.cameraFraming);
  cameraFramingRef.current = nativeRoom.state.cameraFraming;

  const load = useCallback(async () => {
    if (!sessionToken) return;
    setError(null);
    try {
      const [rooms, snapshot] = await Promise.all([
        api.rooms(sessionToken),
        api.participants(sessionToken, route.params.roomId),
      ]);
      setRoom(rooms.rooms.find((item) => item.id === route.params.roomId) ?? null);
      setParticipants(firstArray(snapshot, ['participants']).map((item) => typeof item === 'string' ? item : String((item as { name?: string })?.name ?? '')).filter(Boolean));
      const matching = rooms.rooms.find((item) => item.id === route.params.roomId);
      if (matching?.createdBy && matching.createdBy.toLowerCase() === user?.email?.toLowerCase()) {
        const links = await api.roomGuestLinks(sessionToken, route.params.roomId);
        setGuestLinks(links.links ?? []);
      }
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not load the room.');
    } finally {
      setLoading(false);
    }
  }, [route.params.roomId, sessionToken, user?.email]);

  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (office.event === 'rooms' || office.event === 'participants') void load();
  }, [load, office.event, office.version]);

  const loadSpecialists = useCallback(async () => {
    if (!sessionToken) return;
    setSpecialistsLoading(true);
    setSpecialistsError(null);
    const [specialistResult, agentResult] = await Promise.allSettled([
      api.meetingSpecialists(sessionToken, route.params.roomId),
      api.roomAgents(sessionToken, route.params.roomId),
    ]);
    if (specialistResult.status === 'fulfilled') setSpecialists(specialistResult.value.specialists);
    if (agentResult.status === 'fulfilled') setAgentControlSnapshot(agentResult.value.agents);
    if (specialistResult.status === 'rejected') {
      const err = specialistResult.reason;
      setSpecialistsError(err instanceof BonfireApiError ? err.message : 'Could not load your employee agents.');
    } else if (agentResult.status === 'rejected') {
      const err = agentResult.reason;
      setSpecialistsError(err instanceof BonfireApiError ? err.message : 'Could not load Scout.');
    }
    setSpecialistsLoading(false);
  }, [route.params.roomId, sessionToken]);

  function openSpecialists() {
    setSpecialistsVisible(true);
    void loadSpecialists();
  }

  function requestSpecialist(agentId: string, displayName: string) {
    if (!sessionToken) return;
    Alert.alert(
      `Invite ${displayName}?`,
      'This creates a visible request for this meeting. A person must approve it before the agent can join.',
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Request',
          onPress: () => {
            setSpecialistsPending(true);
            const key = `native_${Date.now().toString(36)}`;
            void api.requestMeetingSpecialist(sessionToken, route.params.roomId, agentId, 'Join this meeting as a specialist', key)
              .then(() => loadSpecialists())
              .catch((err) => setSpecialistsError(err instanceof BonfireApiError ? err.message : 'Could not request the invitation.'))
              .finally(() => setSpecialistsPending(false));
          },
        },
      ],
    );
  }

  function resolveSpecialist(invitation: StrideMeetingSpecialistInvitation, decision: 'approved' | 'declined' | 'dismissed') {
    if (!sessionToken) return;
    const verb = decision === 'approved' ? 'Approve' : decision === 'declined' ? 'Decline' : 'Dismiss';
    Alert.alert(`${verb} ${invitation.displayName || invitation.agentId}?`, decision === 'approved'
      ? 'Approval is bound to this exact invitation. Voice joining remains unavailable until provider qualification.'
      : 'The agent will not join or remain attached to this meeting.', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: verb,
        style: decision === 'approved' ? 'default' : 'destructive',
        onPress: () => {
          setSpecialistsPending(true);
          void api.resolveMeetingSpecialist(sessionToken, route.params.roomId, invitation.id, invitation.revision, decision)
            .then(() => loadSpecialists())
            .catch((err) => setSpecialistsError(err instanceof BonfireApiError ? err.message : 'Could not update the invitation.'))
            .finally(() => setSpecialistsPending(false));
        },
      },
    ]);
  }

  function setRoomScout(action: 'invite' | 'dismiss') {
    if (!sessionToken) return;
    const inviting = action === 'invite';
    Alert.alert(
      inviting ? 'Invite Scout to this call?' : 'Dismiss Scout?',
      inviting
        ? 'Scout will become a visible, audible participant for this sitting. Internal employees follow the company rules of the road; external guests keep their own data choices. Scout’s speech is attributed in the transcript.'
        : 'Scout will leave immediately. Meeting transcription will keep running.',
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: inviting ? 'Invite' : 'Dismiss',
          style: inviting ? 'default' : 'destructive',
          onPress: () => {
            setSpecialistsPending(true);
            void api.setRoomScout(sessionToken, route.params.roomId, action)
              .then((response) => setAgentControlSnapshot(response.agents))
              .catch((err) => setSpecialistsError(err instanceof BonfireApiError ? err.message : 'Could not update Scout.'))
              .finally(() => setSpecialistsPending(false));
          },
        },
      ],
    );
  }

  const canManage = Boolean(room?.createdBy && room.createdBy.toLowerCase() === user?.email?.toLowerCase());
  const inNativeRoom = nativeRoom.state.lifecycle !== 'idle' && nativeRoom.state.lifecycle !== 'joining';
  const alreadyJoinedElsewhere = Boolean(user?.name && participants.some((participant) => (
    normalizedParticipantName(participant) === normalizedParticipantName(user.name)
  )));

  useEffect(() => {
    if (!inNativeRoom) {
      allowCallNavigationRef.current = false;
      return undefined;
    }
    return navigation.addListener('beforeRemove', (event) => {
      if (allowCallNavigationRef.current) return;
      event.preventDefault();
      Alert.alert(
        'Leave this call?',
        'Your camera and microphone will disconnect.',
        [
          { text: 'Stay', style: 'cancel' },
          {
            text: 'Leave call',
            style: 'destructive',
            onPress: () => {
              allowCallNavigationRef.current = true;
              nativeRoom.leave();
              navigation.dispatch(event.data.action);
            },
          },
        ],
      );
    });
  }, [inNativeRoom, navigation, nativeRoom.leave]);
  useEffect(() => {
    const framing = nativeRoom.state.cameraFraming;
    if (!framingActionPendingRef.current || framing.checking || framing.applying) return;
    framingActionPendingRef.current = false;
    const message = framing.message?.trim();
    if (message) Alert.alert('Couldn\u2019t update camera framing', message);
  }, [
    nativeRoom.state.cameraFraming.applying,
    nativeRoom.state.cameraFraming.checking,
    nativeRoom.state.cameraFraming.message,
  ]);
  const remoteFeeds = nativeRoom.state.remoteVideoFeeds;
  const enhancedRoomState = nativeRoom.state as typeof nativeRoom.state & {
    activeSpeaker?: string;
    videoSuspended?: boolean;
  };
  const videoSuspended = Boolean(enhancedRoomState.videoSuspended);
  const activeSpeaker = enhancedRoomState.activeSpeaker;
  const qualityLabel = nativeRoom.state.lifecycle === 'reconnecting'
    ? 'Reconnecting'
    : videoSuspended
      ? 'Restoring video'
    : nativeRoom.state.quality?.label ?? 'Live';
  const callStatusLabel = nativeRoom.state.error
    ?? (nativeRoom.state.screenShareStarting
      ? 'Starting share'
      : nativeRoom.state.screenSharing ? 'Sharing screen' : qualityLabel);
  const localStreamURL = useMemo(() => nativeRoom.state.localStream?.toURL(), [nativeRoom.state.localStream]);
  const localVideoTrackId = nativeRoom.state.localStream?.getVideoTracks()[0]?.id;
  const screenShareStreamURL = useMemo(
    () => nativeRoom.state.screenShareStream?.toURL(),
    [nativeRoom.state.screenShareStream],
  );
  const screenShareVideoTrackId = nativeRoom.state.screenShareStream?.getVideoTracks()[0]?.id;
  const hasLocalVideo = Boolean(nativeRoom.state.localStream?.getVideoTracks().length);
  const localCameraFramingRevision = useMemo(
    () => cameraFramingRenderRevision(nativeRoom.state.cameraFraming),
    [
      nativeRoom.state.cameraFraming.dynamicHeight,
      nativeRoom.state.cameraFraming.dynamicWidth,
      nativeRoom.state.cameraFraming.wideUprightEnabled,
    ],
  );
  const callParticipants = useMemo<CallParticipant[]>(() => {
    const rawRoster = inNativeRoom ? nativeRoom.state.participants : participants;
    const people = presentRemoteVideoParticipants({
      activeSpeaker,
      endpointMediaStates: nativeRoom.state.participantEndpointMediaStates,
      feeds: remoteFeeds.map((feed) => ({
        trackId: feed.trackId,
        participant: feed.participant,
        endpointId: feed.endpointId,
        streamURL: feed.stalled ? '' : feed.stream.toURL(),
      })),
      localNames: [user?.name ?? '', user?.email ?? '', user?.email?.split('@')[0] ?? ''],
      mediaStates: nativeRoom.state.participantMediaStates,
      roster: rawRoster,
    });
    return people;
  }, [
    activeSpeaker,
    inNativeRoom,
    nativeRoom.state.participantEndpointMediaStates,
    nativeRoom.state.participantMediaStates,
    nativeRoom.state.participants,
    participants,
    remoteFeeds,
    user?.email,
    user?.name,
  ]);
  const focusedParticipant = focusedVideoParticipant(callParticipants, pinnedParticipantKey);
  const presentedParticipants = useMemo(() => {
    const stageParticipants = videoStageParticipants(callParticipants);
    const stageFocus = focusedParticipant?.streamURL
      ? focusedParticipant
      : focusedVideoParticipant(stageParticipants, null);
    if (!stageFocus) return stageParticipants;
    return [
      stageFocus,
      ...stageParticipants.filter((participant) => participant.key !== stageFocus.key),
    ];
  }, [callParticipants, focusedParticipant]);
  const pictureInPictureParticipantKey = useMemo(
    () => pictureInPictureParticipant(callParticipants, pinnedParticipantKey)?.key ?? null,
    [callParticipants, pinnedParticipantKey],
  );
  useEffect(() => {
    if (pinnedVideoParticipantIsStale(pinnedParticipantKey, callParticipants)) {
      setPinnedParticipantKey(null);
    }
  }, [callParticipants, pinnedParticipantKey]);
  const liveParticipantCount = Math.max(
    1,
    (inNativeRoom ? nativeRoom.state.participants.length : participants.length) + nativeRoom.state.agentParticipants.length,
  );
  const participantRows = useMemo<RoomParticipantRow[]>(() => [
    {
      key: 'local-device',
      name: user?.name ?? 'You',
      active: false,
      micMuted: nativeRoom.state.muted,
      screenSharing: nativeRoom.state.screenSharing,
      videoOff: nativeRoom.state.screenSharing ? false : nativeRoom.state.cameraOff || videoSuspended,
      local: true,
    },
    ...presentRemoteParticipantDevices({
      endpointMediaStates: nativeRoom.state.participantEndpointMediaStates,
      localNames: [user?.name ?? '', user?.email ?? '', user?.email?.split('@')[0] ?? ''],
      participants: callParticipants,
    }).map((participant) => ({ ...participant })),
  ], [
    callParticipants,
    nativeRoom.state.cameraOff,
    nativeRoom.state.muted,
    nativeRoom.state.participantEndpointMediaStates,
    nativeRoom.state.screenSharing,
    user?.email,
    user?.name,
    videoSuspended,
  ]);
  const spokenTranscriptEntries = useMemo(() => (
    nativeRoom.conversation.transcriptEntries.filter((entry) => entry.source !== 'room_chat')
  ), [nativeRoom.conversation.transcriptEntries]);
  const hasVisibleCallVideo = presentedParticipants.some((participant) => Boolean(participant.streamURL))
    || Boolean(nativeRoom.state.screenSharing && screenShareStreamURL)
    || (hasLocalVideo && !nativeRoom.state.cameraOff && !videoSuspended);
  const selectParticipant = useCallback((participant: CallParticipant) => {
    void Haptics.selectionAsync();
    setPinnedParticipantKey((current) => current === participant.key ? null : participant.key);
  }, []);
  const toggleMuted = useCallback(() => {
    nativeRoom.setMuted(nativeRoom.state.microphoneStarting ? true : !nativeRoom.state.muted);
  }, [nativeRoom.setMuted, nativeRoom.state.microphoneStarting, nativeRoom.state.muted]);
  const toggleCamera = useCallback(() => {
    nativeRoom.setCameraOff(nativeRoom.state.cameraStarting ? true : !nativeRoom.state.cameraOff);
  }, [nativeRoom.setCameraOff, nativeRoom.state.cameraOff, nativeRoom.state.cameraStarting]);
  const toggleRecording = useCallback(() => {
    nativeRoom.setRecording(!nativeRoom.state.recording);
  }, [nativeRoom.setRecording, nativeRoom.state.recording]);
  const openConversation = useCallback((mode: RoomConversationMode) => {
    setConversationMode(mode);
    setConversationVisible(true);
    nativeRoom.setRoomChatOpen(mode === 'chat');
  }, [nativeRoom.setRoomChatOpen]);
  const changeConversationMode = useCallback((mode: RoomConversationMode) => {
    setConversationMode(mode);
    nativeRoom.setRoomChatOpen(mode === 'chat');
  }, [nativeRoom.setRoomChatOpen]);
  const closeConversation = useCallback(() => {
    setConversationVisible(false);
    nativeRoom.setRoomChatOpen(false);
  }, [nativeRoom.setRoomChatOpen]);

  function completeJoin(withVideo: boolean, withAudio: boolean, transferExisting: boolean) {
    if (!room?.passcodeRequired) {
      void nativeRoom.join(withVideo, '', withAudio, transferExisting);
      return;
    }
    Alert.prompt(
      'Room passcode',
      `Enter the passcode for ${room.name}.`,
      (passcode) => {
        if (passcode.trim()) void nativeRoom.join(withVideo, passcode, withAudio, transferExisting);
      },
      'secure-text',
    );
  }

  function joinRoom(withVideo: boolean, withAudio = true) {
    if (!alreadyJoinedElsewhere) {
      completeJoin(withVideo, withAudio, false);
      return;
    }
    Alert.alert(
      'Already in this room',
      `You're connected to ${room?.name ?? 'this room'} on another device. Transfer the call here, or keep both devices connected.`,
      [
        { text: 'Cancel', style: 'cancel' },
        { text: 'Add this device', onPress: () => completeJoin(withVideo, withAudio, false) },
        {
          text: 'Transfer here',
          isPreferred: true,
          onPress: () => completeJoin(withVideo, withAudio, true),
        },
      ],
    );
  }

  function editPasscode() {
    Alert.prompt(
      room?.passcodeRequired ? 'Change room passcode' : 'Protect this room',
      'Leave this blank to remove the passcode.',
      (value) => {
        if (!sessionToken) return;
        void api.setRoomPasscode(sessionToken, route.params.roomId, value)
          .then(() => load())
          .catch((err) => setError(err instanceof BonfireApiError ? err.message : 'Could not update the passcode.'));
      },
      'secure-text',
    );
  }

  function createGuestLink() {
    Alert.prompt('Create guest link', 'Give this one-time link a label.', (label) => {
      if (!sessionToken) return;
      void api.createRoomGuestLink(sessionToken, route.params.roomId, label || 'Guest link')
        .then(async (result) => {
          const url = new URL(result.url, API_BASE_URL).toString();
          await Share.share({ message: `Join ${room?.name ?? 'the room'} in Stride: ${url}`, url });
          await load();
        })
        .catch((err) => setError(err instanceof BonfireApiError ? err.message : 'Could not create a guest link.'));
    });
  }

  function shareMemberRoomLink() {
    const url = new URL('/', API_BASE_URL);
    url.searchParams.set('room', route.params.roomId);
    void Share.share({
      message: `Join ${room?.name ?? 'the room'} in Stride: ${url.toString()}`,
      url: url.toString(),
    });
  }

  function inviteToRoom() {
    if (!canManage) {
      shareMemberRoomLink();
      return;
    }
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title: `Invite to ${room?.name ?? 'this room'}`,
        message: 'Member links require sign-in. Guest links can be revoked from room settings.',
        options: ['Cancel', 'Share member link', 'Create guest link'],
        cancelButtonIndex: 0,
      },
      (index) => {
        if (index === 1) shareMemberRoomLink();
        if (index === 2) createGuestLink();
      },
    );
  }

  function showSystemScreenPicker() {
    if (Platform.OS !== 'ios') throw new Error('Screen sharing is available on iPhone and iPad.');
    const reactTag = findNodeHandle(screenCapturePickerRef.current);
    const manager = NativeModules.ScreenCapturePickerViewManager as {
      show?: (tag: number) => void;
    } | undefined;
    if (!reactTag || typeof manager?.show !== 'function') {
      throw new Error('Screen sharing is unavailable in this build.');
    }
    manager.show(reactTag);
  }

  function requestScreenShare() {
    Alert.alert(
      'Share your screen?',
      'Everything visible on your screen—including notifications—may be shared. Your camera pauses and returns when you stop.',
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Continue',
          isPreferred: true,
          onPress: () => {
            if (!nativeRoom.startScreenShare(showSystemScreenPicker)) {
              Alert.alert('Screen sharing unavailable', 'Wait for the call to finish connecting, then try again.');
            }
          },
        },
      ],
    );
  }

  function setAutoFrameFromCallMenu() {
    const framing = cameraFramingRef.current;
    if (!framing.centerStageSupported || framing.checking || framing.applying) return;
    framingActionPendingRef.current = true;
    nativeRoom.setCenterStageEnabled(!framing.centerStageEnabled);
  }

  function applyWideFraming(enabled: boolean) {
    const framing = cameraFramingRef.current;
    if (!framing.wideUprightSupported || framing.checking || framing.applying) return;
    framingActionPendingRef.current = true;
    nativeRoom.setWideUprightFramingEnabled(enabled);
  }

  function setWideFramingFromCallMenu() {
    const framing = cameraFramingRef.current;
    if (!framing.wideUprightSupported || framing.checking || framing.applying) return;
    if (framing.wideUprightEnabled) {
      applyWideFraming(false);
      return;
    }
    if (wideFramingExplainedRef.current) {
      applyWideFraming(true);
      return;
    }
    Alert.alert(
      'Landscape while upright',
      'On supported iPhones, Stride can send a landscape view while you keep your phone upright. Your video may briefly reframe when it turns on.',
      [
        { text: 'Not now', style: 'cancel' },
        {
          text: 'Turn on',
          isPreferred: true,
          onPress: () => {
            wideFramingExplainedRef.current = true;
            const latest = cameraFramingRef.current;
            if (!latest.wideUprightEnabled) applyWideFraming(true);
          },
        },
      ],
    );
  }

  function showCameraFramingActions() {
    const framing = cameraFramingRef.current;
    const framingBusy = framing.checking || framing.applying;
    const actions: InCallActionDescriptor[] = [
      { id: 'cancel', label: 'Cancel', cancel: true },
    ];
    if (framing.centerStageSupported) {
      const status = centerStageControlStatus(framing);
      actions.push({
        id: 'center-stage',
        label: `Center Stage · ${status}`,
        disabled: framingBusy,
        onSelect: setAutoFrameFromCallMenu,
      });
    }
    if (framing.wideUprightSupported) {
      const status = framing.pendingControl === 'wideUpright'
        ? 'Updating…'
        : framing.wideUprightEnabled ? 'On' : 'Off';
      actions.push({
        id: 'landscape-upright',
        label: `Landscape while upright · ${status}`,
        disabled: framingBusy,
        onSelect: setWideFramingFromCallMenu,
      });
    }
    if (actions.length === 1) return;

    const cancelButtonIndex = actions.findIndex((action) => action.cancel);
    const disabledButtonIndices = actions.flatMap((action, index) => action.disabled ? [index] : []);
    const message = framing.centerStageSupported && framing.wideUprightSupported
      ? 'Center Stage follows faces and widens for more people. Landscape while upright sends a wide frame without rotating your phone.'
      : framing.centerStageSupported
        ? 'Center Stage follows faces and widens when more people enter the frame.'
        : 'Send a landscape frame while keeping your phone comfortably upright.';
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title: 'Camera framing',
        message,
        options: actions.map((action) => action.label),
        cancelButtonIndex,
        disabledButtonIndices,
      },
      (index) => {
        const action = actions[index];
        if (!action || action.cancel || action.disabled) return;
        if (cameraFramingRef.current.checking || cameraFramingRef.current.applying) return;
        action.onSelect?.();
      },
    );
  }

  function showInCallActions() {
    const framing = cameraFramingRef.current;
    // Camera discovery must never block unrelated call tools. A live front
    // camera is normally checked as soon as it starts; this opportunistic
    // refresh only makes framing appear on a later menu open if capture was
    // not ready yet.
    if (!framing.checked && !framing.checking && !framing.applying) {
      nativeRoom.refreshCameraFraming();
    }
    const shareAction = nativeRoom.state.screenSharing
      ? 'Stop sharing screen'
      : nativeRoom.state.screenShareStarting
        ? 'Cancel screen share'
        : 'Share your screen';
    const actions: InCallActionDescriptor[] = [
      { id: 'cancel', label: 'Cancel', cancel: true },
      {
        id: 'screen-share',
        label: shareAction,
        destructive: nativeRoom.state.screenSharing,
        onSelect: () => {
          if (nativeRoom.state.screenSharing || nativeRoom.state.screenShareStarting) {
            nativeRoom.stopScreenShare(nativeRoom.state.screenShareStarting ? 'cancelled' : 'user');
          } else {
            requestScreenShare();
          }
        },
      },
      {
        id: 'recording',
        label: nativeRoom.state.recording ? 'Stop transcript recording' : 'Start transcript recording',
        onSelect: toggleRecording,
      },
    ];
    if (framing.centerStageSupported || framing.wideUprightSupported) {
      actions.push({
        id: 'camera-framing',
        label: framing.applying ? 'Camera framing · Updating…' : 'Camera framing…',
        disabled: framing.checking || framing.applying,
        onSelect: showCameraFramingActions,
      });
    }
    actions.push(
      { id: 'people', label: 'People in this room', onSelect: () => setParticipantsVisible(true) },
      { id: 'specialists', label: 'Agent team', onSelect: openSpecialists },
      { id: 'invite', label: 'Invite someone', onSelect: inviteToRoom },
      {
        id: 'workspace',
        label: 'Open advanced workspace',
        onSelect: () => {
          navigation.navigate('OSWeb', {
            path: `/?room=${encodeURIComponent(route.params.roomId)}`,
            title: `${room?.name ?? route.params.title} · workspace`,
          });
        },
      },
    );
    const cancelButtonIndex = actions.findIndex((action) => action.cancel);
    const destructiveButtonIndices = actions.flatMap((action, index) => action.destructive ? [index] : []);
    const disabledButtonIndices = actions.flatMap((action, index) => action.disabled ? [index] : []);
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title: room?.name ?? route.params.title,
        message: 'The call stays connected while you use these tools.',
        options: actions.map((action) => action.label),
        cancelButtonIndex,
        destructiveButtonIndex: destructiveButtonIndices.length ? destructiveButtonIndices : undefined,
        disabledButtonIndices,
      },
      (index) => {
        const action = actions[index];
        if (!action || action.cancel || action.disabled) return;
        if (action.id === 'camera-framing'
          && (cameraFramingRef.current.checking || cameraFramingRef.current.applying)) return;
        action.onSelect?.();
      },
    );
  }

  function manageGuestLinks() {
    if (!guestLinks.length) {
      Alert.alert('No active guest links', 'Create one from the room menu when you need to invite someone.');
      return;
    }
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title: 'Revoke a guest link',
        options: ['Cancel', ...guestLinks.map((link) => link.label || 'Guest link')],
        cancelButtonIndex: 0,
        destructiveButtonIndex: guestLinks.map((_, index) => index + 1),
      },
      (index) => {
        const link = guestLinks[index - 1];
        if (!link || !sessionToken) return;
        void api.revokeRoomGuestLink(sessionToken, route.params.roomId, link.id)
          .then(() => load())
          .catch((err) => setError(err instanceof BonfireApiError ? err.message : 'Could not revoke the link.'));
      },
    );
  }

  function archiveOrRestore() {
    if (!sessionToken || !room) return;
    const action = room.archived ? 'Restore' : 'Archive';
    Alert.alert(`${action} ${room.name}?`, room.archived ? 'The room will be joinable again.' : 'Anyone inside will be asked to leave safely.', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: action,
        style: room.archived ? 'default' : 'destructive',
        onPress: () => {
          const request = room.archived ? api.restoreRoom(sessionToken, room.id) : api.archiveRoom(sessionToken, room.id);
          void request.then(() => navigation.goBack()).catch((err) => setError(err instanceof BonfireApiError ? err.message : `Could not ${action.toLowerCase()} the room.`));
        },
      },
    ]);
  }

  function showRoomActions() {
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title: room?.name,
        options: ['Cancel', room?.passcodeRequired ? 'Change or remove passcode' : 'Add a passcode', 'Create guest link', 'Revoke guest link', room?.archived ? 'Restore room' : 'Archive room'],
        cancelButtonIndex: 0,
        destructiveButtonIndex: room?.archived ? 3 : [3, 4],
      },
      (index) => {
        if (index === 1) editPasscode();
        if (index === 2) createGuestLink();
        if (index === 3) manageGuestLinks();
        if (index === 4) archiveOrRestore();
      },
    );
  }

  if (inNativeRoom) {
    const bottomInset = Math.max(safeArea.bottom, space[3]);
    const connectionHealthy = qualityLabel === 'Live' && !nativeRoom.state.error;
    const connectionCritical = Boolean(nativeRoom.state.error)
      || qualityLabel === 'Reconnecting'
      || qualityLabel === 'Connection weak';
    return (
      <Screen scroll={false} style={styles.callScreen}>
        <StatusBar style="light" />
        <View style={styles.callSurface}>
          {hasVisibleCallVideo ? <LiveVideoWakeLock /> : null}
          <CallLayout
            agentBenchVisible={nativeRoom.state.agentParticipants.length > 0}
            audioParticipantCount={liveParticipantCount}
            bottomInset={bottomInset}
            landscape={window.width > window.height}
            localCameraOff={nativeRoom.state.cameraOff}
            localCameraFramingRevision={localCameraFramingRevision}
            localName={user?.name ?? 'You'}
            localScreenShareURL={screenShareStreamURL}
            localScreenShareTrackId={screenShareVideoTrackId}
            localScreenSharing={nativeRoom.state.screenSharing}
            localStreamURL={localStreamURL}
            localVideoTrackId={localVideoTrackId}
            localVideoVisible={hasLocalVideo && !nativeRoom.state.cameraOff}
            localVideoSuspended={videoSuspended}
            onSelectParticipant={selectParticipant}
            onSwitchCamera={nativeRoom.switchCamera}
            participants={presentedParticipants}
            pictureInPictureParticipantKey={pictureInPictureParticipantKey}
            pinnedParticipantKey={pinnedParticipantKey}
          />
          <RoomAgentBench
            agents={nativeRoom.state.agentParticipants}
            bottom={bottomInset + 82}
          />
          {Platform.OS === 'ios' ? (
            <View pointerEvents="none" style={styles.screenCapturePicker}>
              <ScreenCapturePickerView ref={screenCapturePickerRef} />
            </View>
          ) : null}

          <View style={styles.callHeader} pointerEvents="box-none">
            <View style={styles.callRoomPill}>
              <Pressable
                accessibilityHint="Shows participants, devices, and media status"
                accessibilityLabel={`View ${liveParticipantCount} participants in ${room?.name ?? route.params.title}`}
                accessibilityRole="button"
                onPress={() => setParticipantsVisible(true)}
                style={({ pressed }) => [styles.callRoomIdentity, pressed && styles.callRoomIdentityPressed]}
              >
                <Text numberOfLines={1} style={styles.callRoomName}>{room?.name ?? route.params.title}</Text>
                <Text numberOfLines={1} style={styles.callParticipantCount}>{liveParticipantCount} here</Text>
              </Pressable>
            </View>
            <View
              accessible
              accessibilityLabel={`Call status: ${callStatusLabel}`}
              accessibilityRole="summary"
              style={styles.callStatusPill}
            >
              <View style={[
                styles.callStatusDot,
                !connectionHealthy && styles.callStatusDotWarning,
                connectionCritical && styles.callStatusDotCritical,
              ]} />
              <Text numberOfLines={2} style={styles.callStatusText}>{callStatusLabel}</Text>
            </View>
          </View>

          {/* CallLayout already owns the inline self-view when two or more
              remote participants are present. Keep the floating preview only
              for the one-remote layout so the local camera is never rendered
              twice as two separate "You" tiles. */}
          {presentedParticipants.length === 1
            && (nativeRoom.state.screenSharing || (hasLocalVideo && !nativeRoom.state.cameraOff)) ? (
            <LocalPreview
              bottom={bottomInset + 96 + (nativeRoom.state.agentParticipants.length > 0 ? 70 : 0)}
              cameraOff={nativeRoom.state.cameraOff}
              framingRevision={localCameraFramingRevision}
              name={user?.name ?? 'You'}
              onSwitchCamera={nativeRoom.switchCamera}
              screenSharing={nativeRoom.state.screenSharing}
              streamURL={nativeRoom.state.screenSharing ? screenShareStreamURL : localStreamURL}
              suspended={videoSuspended}
              videoTrackId={nativeRoom.state.screenSharing ? screenShareVideoTrackId : localVideoTrackId}
            />
          ) : null}

          <View accessibilityLabel="Call controls" style={[styles.callControlDock, { bottom: bottomInset }]}>
            <CallControl
              accessibilityLabel={nativeRoom.state.microphoneStarting
                ? 'Cancel starting microphone'
                : nativeRoom.state.muted ? 'Unmute microphone' : 'Mute microphone'}
              icon={nativeRoom.state.muted || nativeRoom.state.microphoneStarting ? 'mic.slash.fill' : 'mic.fill'}
              label={nativeRoom.state.microphoneStarting ? 'Cancel' : nativeRoom.state.muted ? 'Unmute' : 'Mute'}
              onPress={toggleMuted}
              tone={nativeRoom.state.muted || nativeRoom.state.microphoneStarting ? 'off' : 'default'}
            />
            <CallControl
              accessibilityLabel={nativeRoom.state.screenSharing || nativeRoom.state.screenShareStarting
                ? 'Camera paused while sharing screen'
                : nativeRoom.state.cameraStarting
                ? 'Cancel starting camera'
                : nativeRoom.state.cameraOff ? 'Turn camera on' : 'Turn camera off'}
              disabled={nativeRoom.state.screenSharing || nativeRoom.state.screenShareStarting}
              icon={nativeRoom.state.screenSharing || nativeRoom.state.screenShareStarting || nativeRoom.state.cameraOff || nativeRoom.state.cameraStarting ? 'video.slash.fill' : 'video.fill'}
              label={nativeRoom.state.screenSharing || nativeRoom.state.screenShareStarting ? 'Paused' : nativeRoom.state.cameraStarting ? 'Cancel' : nativeRoom.state.cameraOff ? 'Start video' : 'Stop video'}
              onPress={toggleCamera}
              tone={nativeRoom.state.screenSharing || nativeRoom.state.screenShareStarting || nativeRoom.state.cameraOff || nativeRoom.state.cameraStarting ? 'off' : 'default'}
            />
            <CallControl
              accessibilityLabel={nativeRoom.conversation.unreadCount
                ? `Room chat, ${nativeRoom.conversation.unreadCount} unread`
                : 'Open room chat'}
              badge={nativeRoom.conversation.unreadCount}
              icon="bubble.left.and.bubble.right.fill"
              label="Chat"
              onPress={() => openConversation('chat')}
            />
            <CallControl
              accessibilityLabel="More call options"
              icon="ellipsis"
              label="More"
              onPress={showInCallActions}
            />
            <CallControl
              accessibilityLabel="Leave room"
              icon="phone.down.fill"
              label="Leave"
              onPress={nativeRoom.leave}
              tone="danger"
            />
          </View>

          <RoomConversationSheet
            messages={[...nativeRoom.conversation.messages]}
            mode={conversationMode}
            onClose={closeConversation}
            onDeleteMessage={nativeRoom.deleteRoomChat}
            onModeChange={changeConversationMode}
            onSendMessage={nativeRoom.sendRoomChat}
            roomName={room?.name ?? route.params.title}
            transcriptEntries={[...spokenTranscriptEntries]}
            viewer={{ email: user?.email, name: user?.name }}
            visible={conversationVisible}
          />
          <RoomParticipantsSheet
            onClose={() => setParticipantsVisible(false)}
            onInvite={inviteToRoom}
            participants={participantRows}
            roomName={room?.name ?? route.params.title}
            visible={participantsVisible}
          />
          <RoomSpecialistsSheet
            agents={inNativeRoom ? nativeRoom.state.agentParticipants : agentControlSnapshot}
            error={specialistsError}
            loading={specialistsLoading}
            onClose={() => setSpecialistsVisible(false)}
            onRequest={requestSpecialist}
            onResolve={resolveSpecialist}
            onSetScout={setRoomScout}
            pending={specialistsPending}
            status={specialists}
            visible={specialistsVisible}
          />
        </View>
      </Screen>
    );
  }

  return (
    <Screen
      title={room?.name ?? route.params.title}
      subtitle={room?.live ? 'Live now' : room?.archived ? 'Archived' : 'No one here yet'}
      loading={loading}
      error={error}
      onRetry={() => void load()}
      right={canManage ? (
        <Pressable accessibilityRole="button" accessibilityLabel="Room settings" onPress={showRoomActions} style={styles.manage}>
          <SymbolView name="ellipsis" tintColor={colors.text1} size={20} />
        </Pressable>
      ) : undefined}
    >
      {!inNativeRoom ? (
        <View style={[styles.joinCard, shadow[1]]}>
          <View style={styles.joinHeading}>
            <View style={[styles.joinOrb, room?.live && styles.orbLive]}>
              <SymbolView name={room?.live ? 'waveform.circle.fill' : 'person.2.fill'} tintColor={room?.live ? colors.live : colors.text1} size={28} />
            </View>
            <View style={styles.joinCopy}>
              <Text style={styles.joinTitle}>{room?.live ? `${room.participantCount} here now` : 'Start the conversation'}</Text>
              <Text style={styles.joinBody}>Join visibly. Your camera starts on and your microphone stays muted.</Text>
            </View>
          </View>
          {nativeRoom.state.lifecycle === 'idle' && !room?.archived ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Join room with camera on and microphone off"
              onPress={() => joinRoom(true, false)}
              style={({ pressed }) => [styles.join, pressed && styles.pressed]}
            >
              <SymbolView name="person.2.fill" tintColor={colors.onAccent} size={18} />
              <Text style={styles.joinText}>Join room</Text>
            </Pressable>
        ) : null}
        {nativeRoom.state.lifecycle === 'joining' ? (
          <View style={styles.joining}><ActivityIndicator color={colors.text1} /><Text style={styles.joiningText}>Preparing your room…</Text></View>
        ) : null}
        {nativeRoom.state.error ? <Text style={styles.roomError}>{nativeRoom.state.error}</Text> : null}
        </View>
      ) : null}

      <Text style={styles.sectionTitle}>People</Text>
      <View style={styles.people}>
        {participants.length ? participants.map((name) => (
          <View key={name} style={styles.person}>
            <View style={styles.initial}><Text style={styles.initialText}>{name.slice(0, 1).toUpperCase()}</Text></View>
            <Text style={styles.personName}>{name}</Text>
          </View>
        )) : <Text style={styles.empty}>No one is seated yet.</Text>}
      </View>

      <View style={styles.facts}>
        <Text style={styles.fact}>{room?.passcodeRequired ? 'Passcode protected' : 'One-tap member access'}</Text>
        <Text style={styles.fact}>{room?.guestLinkActive ? 'Guest link active' : 'No active guest link'}</Text>
        <Pressable
          accessibilityRole="button"
          onPress={() => navigation.navigate('OSWeb', { path: `/?room=${encodeURIComponent(route.params.roomId)}`, title: `${room?.name ?? route.params.title} · advanced` })}
          style={styles.advanced}
        >
          <Text style={styles.advancedText}>Open advanced web room tools</Text>
          <SymbolView name="arrow.up.right" tintColor={colors.text2} size={14} />
        </Pressable>
      </View>
    </Screen>
  );
}

const styles = StyleSheet.create({
  joinCard: { backgroundColor: colors.surface1, borderRadius: radius.xxl, padding: space[5], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  joinHeading: { flexDirection: 'row', alignItems: 'center', gap: space[3], marginBottom: space[4] },
  joinOrb: { width: 56, height: 56, borderRadius: 20, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.surface3 },
  joinCopy: { flex: 1 },
  orbLive: { backgroundColor: colors.liveSoft },
  joinTitle: { ...type.headline, color: colors.text1 },
  joinBody: { ...type.caption, color: colors.text2, marginTop: 3 },
  join: { minHeight: 48, width: '100%', borderRadius: radius.lg, backgroundColor: colors.accent, paddingHorizontal: space[5], flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 9 },
  joinText: { ...type.button, color: colors.onAccent },
  joining: { minHeight: 48, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2] },
  joiningText: { ...type.bodySm, color: colors.text2 },
  roomError: { ...type.caption, color: colors.danger, textAlign: 'center', marginTop: space[2] },
  callScreen: { backgroundColor: ink[950] },
  callSurface: {
    flex: 1,
    marginHorizontal: -space[5],
    marginTop: -space[3],
    overflow: 'hidden',
    backgroundColor: ink[950],
  },
  screenCapturePicker: {
    position: 'absolute',
    top: 0,
    left: 0,
    width: 1,
    height: 1,
    opacity: 0,
  },
  callVideoTile: {
    position: 'relative',
    overflow: 'hidden',
    borderRadius: radius.xl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: ink[850],
  },
  callVideoTileCompact: { borderRadius: radius.lg },
  callVideoTileSharing: { borderColor: 'rgba(48,209,88,0.55)' },
  callVideoTilePressed: { opacity: 0.9, transform: [{ scale: 0.99 }] },
  callVideo: { position: 'absolute', top: 0, right: 0, bottom: 0, left: 0, backgroundColor: ink[900] },
  oneUpTile: { flex: 1, borderRadius: 0 },
  audioOnlyStage: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
    backgroundColor: ink[900],
  },
  audioOnlyMark: {
    width: 68,
    height: 68,
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: space[2],
    borderRadius: 34,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.12)',
    backgroundColor: 'rgba(255,255,255,0.07)',
  },
  audioOnlyTitle: { ...type.headline, color: '#FFFFFF' },
  audioOnlyCopy: { ...type.caption, color: 'rgba(255,255,255,0.56)' },
  oneUpStageSlot: { flex: 1, alignItems: 'center', justifyContent: 'center', overflow: 'hidden' },
  oneUpPrimaryTile: { flex: 0, borderRadius: 0 },
  videoPlaceholder: {
    position: 'absolute',
    top: 0,
    right: 0,
    bottom: 0,
    left: 0,
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[3],
    backgroundColor: ink[850],
  },
  agentBench: {
    position: 'absolute',
    left: space[3],
    right: space[3],
    zIndex: 25,
    minHeight: 60,
    paddingTop: 7,
    paddingBottom: 7,
    borderRadius: 24,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: 'rgba(13,13,16,0.88)',
  },
  agentBenchHeading: {
    height: 14,
    paddingHorizontal: space[3],
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
  },
  agentBenchLabel: {
    color: 'rgba(255,255,255,0.52)',
    fontSize: 9,
    fontFamily: 'GoogleSansFlex_600SemiBold',
    fontWeight: '600',
    lineHeight: 11,
    letterSpacing: 0.72,
  },
  agentBenchList: { gap: 6, paddingHorizontal: 6, paddingTop: 5 },
  agentBenchMember: {
    minWidth: 156,
    height: 34,
    paddingLeft: 10,
    paddingRight: 8,
    borderRadius: 17,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 8,
    backgroundColor: 'rgba(255,255,255,0.07)',
  },
  agentBenchMemberSpeaking: { backgroundColor: 'rgba(255,255,255,0.11)' },
  agentBenchIdentity: { minWidth: 0, flex: 1, flexDirection: 'row', alignItems: 'center', gap: 7 },
  agentBenchDot: { width: 7, height: 7, borderRadius: 3.5 },
  agentBenchCopy: { minWidth: 0, flex: 1 },
  agentBenchName: {
    color: '#FFFFFF',
    fontSize: 11,
    fontFamily: 'GoogleSansFlex_600SemiBold',
    fontWeight: '600',
    lineHeight: 13,
  },
  agentBenchState: {
    marginTop: 1,
    color: 'rgba(255,255,255,0.50)',
    fontSize: 9,
    fontFamily: 'GoogleSansFlex_500Medium',
    fontWeight: '500',
    lineHeight: 10,
  },
  videoAvatar: {
    width: 82,
    height: 82,
    borderRadius: 41,
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: 'rgba(255,255,255,0.09)',
  },
  videoAvatarCompact: { width: 44, height: 44, borderRadius: 22 },
  videoAvatarText: { color: '#FFFFFF', fontSize: 27, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', letterSpacing: -0.6 },
  videoAvatarTextCompact: { fontSize: 16, letterSpacing: -0.2 },
  cameraOffRow: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  cameraOffText: { ...type.caption, color: 'rgba(255,255,255,0.55)' },
  callVideoLabel: {
    position: 'absolute',
    left: space[3],
    bottom: space[3],
    maxWidth: '76%',
    minHeight: 28,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 12,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: 'rgba(5,5,7,0.68)',
  },
  callVideoLabelCompact: { left: 6, bottom: 6, minHeight: 23, maxWidth: '88%', paddingHorizontal: 7, paddingVertical: 3, borderRadius: 9 },
  callVideoLabelMeasuring: { opacity: 0 },
  callVideoLabelText: { ...type.captionMedium, flexShrink: 1, color: '#FFFFFF' },
  callVideoLabelTextCompact: { fontSize: 10, lineHeight: 13 },
  speakerDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: '#30D158' },
  screenShareBadge: {
    position: 'absolute',
    top: space[3],
    alignSelf: 'center',
    minHeight: 26,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    paddingHorizontal: 9,
    borderRadius: 12,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(48,209,88,0.36)',
    backgroundColor: 'rgba(5,5,7,0.76)',
  },
  screenShareBadgeText: { fontSize: 9, fontFamily: 'GoogleSansFlex_700Bold', fontWeight: '700', letterSpacing: 0.75, color: '#30D158' },
  localPreview: {
    ...shadow.mark,
    position: 'absolute',
    right: space[3],
    zIndex: 20,
    width: 100,
    height: 138,
    overflow: 'hidden',
    borderRadius: 18,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: ink[850],
  },
  localPreviewInline: { position: 'relative', right: undefined, zIndex: 1 },
  localPlaceholder: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
    backgroundColor: ink[800],
  },
  localPreviewStarting: {
    position: 'absolute',
    top: 0,
    right: 0,
    bottom: 0,
    left: 0,
    zIndex: 3,
    alignItems: 'center',
    justifyContent: 'center',
    gap: 6,
    backgroundColor: ink[850],
  },
  localPreviewStartingText: { color: 'rgba(255,255,255,0.76)', fontSize: 9, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600' },
  localAvatarText: { color: '#FFFFFF', fontSize: 24, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', letterSpacing: -0.4 },
  localLabelPill: {
    position: 'absolute',
    left: 7,
    bottom: 7,
    maxWidth: 82,
    paddingHorizontal: 7,
    paddingVertical: 3,
    borderRadius: 8,
    backgroundColor: 'rgba(5,5,7,0.68)',
  },
  localLabelText: { color: '#FFFFFF', fontSize: 10, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', lineHeight: 13 },
  switchCamera: {
    position: 'absolute',
    top: 6,
    right: 6,
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 22,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.14)',
    backgroundColor: 'rgba(5,5,7,0.68)',
  },
  switchCameraPressed: { transform: [{ scale: 0.94 }], backgroundColor: 'rgba(5,5,7,0.84)' },
  largeCallShell: {
    flex: 1,
    gap: space[2],
    paddingTop: 78,
    paddingHorizontal: space[2],
  },
  largeCallShellLandscape: { flexDirection: 'row', paddingTop: 58 },
  primaryStageSlot: { flex: 1, alignItems: 'center', justifyContent: 'center', overflow: 'hidden' },
  largePrimaryTile: { flex: 0 },
  largePrimaryCameraTile: { flex: 1, alignSelf: 'stretch' },
  participantRail: { height: 104, flexDirection: 'row', gap: space[2] },
  participantRailLandscape: { width: 108, height: '100%', flexDirection: 'column' },
  participantStrip: { flex: 1, height: 104 },
  participantStripLandscape: { width: 108, height: undefined },
  participantStripContent: { gap: space[2], paddingRight: space[2] },
  participantStripContentLandscape: { paddingRight: 0, paddingBottom: space[2] },
  participantStripTile: { width: 126, height: 96 },
  participantStripTileLandscape: { width: 108, height: 80 },
  callHeader: {
    position: 'absolute',
    top: space[3],
    left: space[3],
    right: space[3],
    zIndex: 30,
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: space[2],
  },
  callRoomPill: { flex: 1, minWidth: 0, alignItems: 'flex-start' },
  callRoomIdentity: {
    maxWidth: '100%',
    minHeight: 42,
    justifyContent: 'center',
    paddingHorizontal: space[3],
    paddingVertical: 6,
    borderRadius: 18,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: 'rgba(7,7,8,0.76)',
  },
  callRoomIdentityPressed: { backgroundColor: 'rgba(7,7,8,0.88)', transform: [{ scale: 0.99 }] },
  callRoomName: { color: '#FFFFFF', fontSize: 13, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', lineHeight: 16, letterSpacing: -0.08 },
  callParticipantCount: { marginTop: 1, color: 'rgba(255,255,255,0.58)', fontSize: 10, fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500', lineHeight: 13, fontVariant: ['tabular-nums'] },
  callStatusPill: {
    minHeight: 42,
    maxWidth: '48%',
    flexDirection: 'row',
    alignItems: 'center',
    gap: 7,
    paddingHorizontal: space[3],
    borderRadius: 18,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: 'rgba(7,7,8,0.76)',
  },
  callStatusDot: { width: 8, height: 8, borderRadius: 4, backgroundColor: '#30D158' },
  callStatusDotWarning: { backgroundColor: '#FF9F0A' },
  callStatusDotCritical: { backgroundColor: '#FF6B63' },
  callStatusText: { flexShrink: 1, color: '#FFFFFF', fontSize: 11, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', lineHeight: 14 },
  callControlDock: {
    ...shadow.mark,
    position: 'absolute',
    left: space[3],
    right: space[3],
    zIndex: 30,
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    padding: space[2],
    borderRadius: radius.xxl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(255,255,255,0.10)',
    backgroundColor: 'rgba(13,13,16,0.92)',
  },
  callControl: {
    flex: 1,
    minWidth: 44,
    height: 56,
    alignItems: 'center',
    justifyContent: 'center',
    gap: 3,
    borderRadius: 20,
    backgroundColor: 'rgba(255,255,255,0.09)',
  },
  callControlOff: { backgroundColor: '#FFFFFF' },
  callControlRecording: { backgroundColor: 'rgba(255,69,58,0.15)' },
  callControlDanger: { backgroundColor: '#FF453A' },
  callControlDisabled: { opacity: 0.42 },
  callControlPressed: { transform: [{ scale: 0.96 }] },
  callControlLabel: { color: '#FFFFFF', fontSize: 10, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', lineHeight: 12 },
  callControlLabelInverted: { color: ink[950] },
  callControlBadge: {
    position: 'absolute',
    top: 5,
    right: 5,
    minWidth: 18,
    height: 18,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 4,
    borderRadius: 9,
    borderWidth: 2,
    borderColor: 'rgba(13,13,16,0.92)',
    backgroundColor: '#FF453A',
  },
  callControlBadgeText: { color: '#FFFFFF', fontSize: 9, fontFamily: 'GoogleSansFlex_700Bold', fontWeight: '700', lineHeight: 11 },
  pressed: { transform: [{ scale: 0.96 }], opacity: 0.9 },
  sectionTitle: { ...type.label, color: colors.text3, textTransform: 'uppercase', marginTop: space[6], marginBottom: space[3] },
  people: { backgroundColor: colors.surface1, borderRadius: radius.xl, padding: space[4] },
  person: { minHeight: 48, flexDirection: 'row', alignItems: 'center', gap: space[3] },
  initial: { width: 34, height: 34, borderRadius: 12, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.surface3 },
  initialText: { ...type.headline, color: colors.text1 },
  personName: { ...type.bodyMedium, color: colors.text1 },
  empty: { ...type.bodySm, color: colors.text2 },
  facts: { marginTop: space[4], gap: space[2] },
  fact: { ...type.caption, color: colors.text2 },
  advanced: { minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6, marginTop: space[2] },
  advancedText: { ...type.caption, color: colors.text2, textDecorationLine: 'underline' },
  manage: { width: 44, height: 44, borderRadius: radius.md, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
});
